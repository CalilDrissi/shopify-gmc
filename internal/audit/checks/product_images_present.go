package checks

import (
	"context"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaProductImagesPresent,
		Run:          runProductImagesPresent,
		Instructions: instructionsProductImagesPresent,
	})
}

var metaProductImagesPresent = audit.Meta{
	ID:              "product_images_present",
	Title:           "Each product has at least one image",
	Category:        "content",
	DefaultSeverity: audit.SeverityError,
	AIFixEligible:   false, // photo upload, not a copy fix
}

func runProductImagesPresent(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaProductImagesPresent)
	var issues []audit.Issue
	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		if hasProductImage(p) {
			continue
		}
		issues = append(issues, audit.Issue{
			URL:          p.URL,
			ProductTitle: productTitle(p),
			Detail:       "No product image found in JSON-LD or in the page's product gallery.",
		})
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

// hasProductImage returns true if the page declares any product image, either
// via JSON-LD's image field or via an <img src="…cdn.shopify.com…"> element.
func hasProductImage(p *audit.Page) bool {
	if product, ok := extractProductSchema(p.Doc); ok {
		if imageFieldOK(product["image"]) {
			return true
		}
	}
	found := false
	p.Doc.Find("img[src]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		src, _ := s.Attr("src")
		if strings.Contains(src, "cdn.shopify.com") {
			found = true
			return false
		}
		return true
	})
	return found
}
