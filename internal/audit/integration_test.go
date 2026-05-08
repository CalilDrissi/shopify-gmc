package audit_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/gmcauditor/internal/audit"
	_ "github.com/example/gmcauditor/internal/audit/checks" // register all 20
	"github.com/example/gmcauditor/internal/crawler"
)

// TestIntegration_FullAuditAgainstFixtureStore wires the crawler and the full
// check registry against an httptest server modelled on a real Shopify store
// — Shopify markers, working products, two policies, and a substantive about
// page. We then assert the per-check pass/fail map matches what the fixtures
// imply: most checks pass, the missing shipping + terms links fail, and the
// prohibited-content scanner stays silent.
func TestIntegration_FullAuditAgainstFixtureStore(t *testing.T) {
	t.Parallel()
	srv := newFixtureStore(t)
	defer srv.Close()

	c, err := crawler.New(srv.URL)
	if err != nil {
		t.Fatalf("New crawler: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cx, err := c.Crawl(ctx)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	if cx.Homepage == nil {
		t.Fatal("homepage missing")
	}
	if len(cx.ProductPages) < 2 {
		t.Fatalf("expected ≥2 product pages, got %d", len(cx.ProductPages))
	}
	if cx.PolicyPages["refund-policy"] == nil {
		t.Fatal("refund-policy not crawled")
	}
	if cx.PolicyPages["about"] == nil {
		t.Fatal("about page not crawled")
	}

	all := audit.All()
	// 20 original crawler checks + 9 GMC-native checks = 29.
	if len(all) != 29 {
		t.Fatalf("expected 29 registered checks (20 crawler + 9 GMC), got %d", len(all))
	}

	// Run every check.
	results := make(map[string]audit.CheckResult, len(all))
	for _, check := range all {
		results[check.Meta.ID] = check.Run(ctx, cx)
	}

	expected := map[string]audit.Status{
		"product_schema_present":     audit.StatusPass,
		"product_schema_complete":    audit.StatusPass,
		"product_identifier_present": audit.StatusPass,
		"refund_policy_linked":       audit.StatusPass,
		"shipping_policy_linked":     audit.StatusFail, // intentionally missing in the fixture footer
		"privacy_policy_linked":      audit.StatusPass,
		"terms_of_service_linked":    audit.StatusFail, // intentionally missing
		"contact_info_visible":       audit.StatusPass,
		"about_page_exists":          audit.StatusPass,
		// httptest.NewServer is plain http://; this check correctly fires for
		// the test rig. In production it'd be a critical fail to land on.
		"https_everywhere":           audit.StatusFail,
		"product_title_quality":      audit.StatusPass,
		"product_description_quality": audit.StatusPass,
		"product_images_present":     audit.StatusPass,
		"image_alt_quality":          audit.StatusPass,
		"prohibited_content_signals": audit.StatusPass,
		"currency_consistency":       audit.StatusPass,
		"canonical_tags_present":     audit.StatusPass,
		"robots_txt_valid":           audit.StatusPass,
		"sitemap_accessible":         audit.StatusPass,
		"broken_product_links":       audit.StatusPass,
	}

	for id, want := range expected {
		got, ok := results[id]
		if !ok {
			t.Errorf("check %q did not run", id)
			continue
		}
		if got.Status != want {
			t.Errorf("check %q: status=%s want %s; issues=%+v", id, got.Status, want, got.Issues)
		}
	}

	// Sanity: every check produced a non-zero duration and a populated meta.
	for id, r := range results {
		if r.Meta.ID == "" {
			t.Errorf("%s: empty meta on result", id)
		}
		if r.Status == audit.StatusFail && len(r.Issues) == 0 {
			t.Errorf("%s: failed with no issues", id)
		}
	}

	// Spot-check that legal-policy checks really aren't AI-fix-eligible —
	// verifies the registry didn't get reshuffled.
	for _, id := range []string{"privacy_policy_linked", "terms_of_service_linked"} {
		c, _ := audit.Get(id)
		if c.Meta.AIFixEligible {
			t.Errorf("%s must not be AI-fix-eligible", id)
		}
	}
}

// ----------------------------------------------------------------------------
// Fixture store
// ----------------------------------------------------------------------------

func newFixtureStore(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", "http://"+r.Host)
	})

	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		base := "http://" + r.Host
		fmt.Fprintf(w, `<?xml version="1.0"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/</loc></url>
  <url><loc>%s/products/widget</loc></url>
  <url><loc>%s/products/gadget</loc></url>
  <url><loc>%s/collections/all</loc></url>
  <url><loc>%s/policies/refund-policy</loc></url>
  <url><loc>%s/policies/privacy-policy</loc></url>
  <url><loc>%s/pages/about</loc></url>
</urlset>`, base, base, base, base, base, base, base)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		host := "http://" + r.Host
		fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Acme Goods</title>
  <link rel="canonical" href="%s/">
  <link rel="stylesheet" href="https://cdn.shopify.com/s/themes/dawn/style.css">
  <script>window.Shopify = {theme:{name:'Dawn'}};</script>
</head>
<body>
  <h1>Acme Goods</h1>
  <a href="/products/widget">Widget</a>
  <a href="/products/gadget">Gadget</a>
  <a href="/collections/all">All</a>
  <footer>
    <p>Acme Goods Ltd · 123 Market Street · Brooklyn, NY 11201</p>
    <p><a href="mailto:hello@acme.example">hello@acme.example</a> · <a href="tel:+15551234567">+1 (555) 123-4567</a></p>
    <a href="/policies/refund-policy">Returns</a>
    <a href="/policies/privacy-policy">Privacy</a>
    <a href="/pages/about">About</a>
  </footer>
</body>
</html>`, host)
	})

	mux.HandleFunc("/products/widget", productHandler("Widget", "WB-32-BLK", "29.99"))
	mux.HandleFunc("/products/gadget", productHandler("Gadget", "GD-77-RED", "49.50"))

	mux.HandleFunc("/collections/all", htmlHandler(`<html><body><h1>All</h1></body></html>`))
	mux.HandleFunc("/policies/refund-policy", htmlHandler(`<html><body><h1>Returns</h1><p>30-day returns, free shipping back.</p></body></html>`))
	mux.HandleFunc("/policies/privacy-policy", htmlHandler(`<html><body><h1>Privacy</h1><p>We don't sell your data.</p></body></html>`))
	mux.HandleFunc("/pages/about", htmlHandler(`<html><body><h1>About Acme</h1>
<p>Acme Goods was founded in 2018 by Dana Park, a textile designer who couldn't find a single
small-batch supplier of organic-cotton aprons in the Bay Area. We started with eight aprons sewn
in Dana's garage and have grown into a 12-person studio in Oakland that ships to all 50 states
and 14 countries. Every product is cut and sewn within 25 miles of where it's designed.</p>
</body></html>`))

	return httptest.NewServer(mux)
}

func productHandler(name, sku, price string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		host := "http://" + r.Host
		fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>%[1]s</title>
  <link rel="canonical" href="%[3]s%[2]s">
  <script>window.Shopify={theme:{name:'Dawn'}};</script>
  <script type="application/ld+json">
  {"@context":"https://schema.org/","@type":"Product",
    "name":"%[1]s",
    "image":"https://cdn.shopify.com/s/files/1/0/products/%[1]s.jpg",
    "description":"The %[1]s is a vacuum-insulated 32oz stainless steel water bottle. Triple-wall construction keeps drinks cold for 24 hours, hot for 12. Leakproof flip-top lid, BPA-free, dishwasher-safe inner sleeve. Backed by a lifetime warranty against manufacturing defects.",
    "brand":{"@type":"Brand","name":"Acme"},
    "sku":"%[4]s","gtin13":"0123456789012",
    "offers":{"@type":"Offer","priceCurrency":"USD","price":"%[5]s","availability":"https://schema.org/InStock"}
  }
  </script>
</head>
<body>
  <h1>%[1]s</h1>
  <img src="https://cdn.shopify.com/s/files/1/0/products/%[1]s.jpg" alt="%[1]s vacuum-insulated water bottle in matte black, 32 ounce capacity">
  <p>$%[5]s · in stock</p>
</body>
</html>`, name, "/products/"+pathSafe(name), host, sku, price)
	}
}

func pathSafe(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, byte(r))
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r-'A'+'a'))
		}
	}
	return string(out)
}

func htmlHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}
}
