package checks

import (
	"context"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaHTTPSEverywhere,
		Run:          runHTTPSEverywhere,
		Instructions: instructionsHTTPSEverywhere,
	})
}

var metaHTTPSEverywhere = audit.Meta{
	ID:              "https_everywhere",
	Title:           "Store and outbound resources use HTTPS",
	Category:        "infra",
	DefaultSeverity: audit.SeverityCritical,
	AIFixEligible:   false, // infra fix; not appropriate for AI rewrite
}

func runHTTPSEverywhere(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaHTTPSEverywhere)
	var issues []audit.Issue

	if u, err := url.Parse(cx.StoreURL); err == nil && u.Scheme != "https" {
		issues = append(issues, audit.Issue{
			URL:    cx.StoreURL,
			Detail: "Store URL is not served over HTTPS.",
		})
	}

	pages := []*audit.Page{cx.Homepage}
	pages = append(pages, cx.ProductPages...)
	for _, p := range pages {
		if p == nil || p.Doc == nil {
			continue
		}
		for _, mixed := range mixedContentSelectors(p.Doc) {
			issues = append(issues, audit.Issue{
				URL:    p.URL,
				Detail: "Insecure resource referenced over http://: " + mixed,
			})
		}
	}

	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

// mixedContentSelectors finds <img>, <script>, <link>, and <iframe> elements
// loading over plain http:// — i.e. mixed content.
func mixedContentSelectors(doc *goquery.Document) []string {
	var out []string
	check := func(sel, attr string) {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			v, _ := s.Attr(attr)
			if strings.HasPrefix(strings.ToLower(v), "http://") {
				out = append(out, v)
			}
		})
	}
	check("img[src]", "src")
	check("script[src]", "src")
	check("link[href]", "href")
	check("iframe[src]", "src")
	return out
}
