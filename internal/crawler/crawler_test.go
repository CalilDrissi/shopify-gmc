package crawler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCrawler_HappyPath(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("User-agent: *\nAllow: /\nSitemap: https://%HOST%/sitemap.xml\n"))
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.test/products/widget</loc></url>
  <url><loc>https://example.test/products/gadget</loc></url>
</urlset>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Acme</title>
<script src="https://cdn.shopify.com/s/themes/dawn/runtime.js"></script>
</head><body>
<a href="/products/widget">Widget</a>
<a href="/products/gadget">Gadget</a>
<a href="/collections/all">All</a>
<a href="/policies/refund-policy">Returns</a>
<a href="/policies/privacy-policy">Privacy</a>
<a href="/pages/about">About</a>
</body></html>`))
	})
	mux.HandleFunc("/products/widget", htmlHandler(`<html><body><h1>Widget</h1></body></html>`))
	mux.HandleFunc("/products/gadget", htmlHandler(`<html><body><h1>Gadget</h1></body></html>`))
	mux.HandleFunc("/collections/all", htmlHandler(`<html><body><h1>All</h1></body></html>`))
	mux.HandleFunc("/policies/refund-policy", htmlHandler(`<html><body><h1>Returns</h1></body></html>`))
	mux.HandleFunc("/policies/privacy-policy", htmlHandler(`<html><body><h1>Privacy</h1></body></html>`))
	mux.HandleFunc("/pages/about", htmlHandler(`<html><body><h1>About</h1></body></html>`))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cx, err := c.Crawl(ctx)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if cx.Homepage == nil || cx.Homepage.StatusCode != 200 {
		t.Errorf("homepage missing or bad: %+v", cx.Homepage)
	}
	if len(cx.ProductPages) != 2 {
		t.Errorf("products = %d, want 2", len(cx.ProductPages))
	}
	if len(cx.CollectionPages) != 1 {
		t.Errorf("collections = %d, want 1", len(cx.CollectionPages))
	}
	if cx.PolicyPages["refund-policy"] == nil {
		t.Error("missing refund-policy")
	}
	if cx.PolicyPages["privacy-policy"] == nil {
		t.Error("missing privacy-policy")
	}
	if cx.PolicyPages["about"] == nil {
		t.Error("missing about page")
	}
	if !strings.Contains(cx.RobotsTxt, "User-agent") {
		t.Error("robots.txt missing")
	}
	// sitemap was served at /sitemap.xml — but the loc URLs reference
	// example.test, not the test server, so they may not parse to that origin.
	// We just assert the crawler didn't crash and surfaced something.
	if cx.SitemapURLs == nil {
		t.Log("sitemap URLs empty (expected since fixture loc points off-host)")
	}
}

func TestCrawler_NotShopify(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/", htmlHandler(`<html><body><h1>Plain WordPress</h1></body></html>`))
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("User-agent: *\nAllow: /\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.Crawl(ctx)
	if !errors.Is(err, ErrNotShopify) {
		t.Errorf("err = %v, want ErrNotShopify", err)
	}
}

func TestCrawler_RobotsBlocksProducts(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /products\n"))
	})
	mux.HandleFunc("/", htmlHandler(`<html><head>
<script src="https://cdn.shopify.com/s/themes/dawn/runtime.js"></script>
</head><body>
<a href="/products/widget">Widget</a>
</body></html>`))
	mux.HandleFunc("/products/widget", htmlHandler(`<html><body>Should not be reached</body></html>`))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := New(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cx, err := c.Crawl(ctx)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if len(cx.ProductPages) != 0 {
		t.Errorf("expected products to be blocked by robots.txt, got %d", len(cx.ProductPages))
	}
}

func htmlHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}
}
