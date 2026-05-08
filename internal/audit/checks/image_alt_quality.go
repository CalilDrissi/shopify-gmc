package checks

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaImageAltQuality,
		Run:          runImageAltQuality,
		Instructions: instructionsImageAltQuality,
	})
}

var metaImageAltQuality = audit.Meta{
	ID:              "image_alt_quality",
	Title:           "Product images have descriptive alt text (not filenames or SKUs)",
	Category:        "content",
	DefaultSeverity: audit.SeverityWarning,
	AIFixEligible:   true,
}

// skuLike matches strings that look like a SKU: 4+ chars, all uppercase
// letters/digits/dashes/underscores, no spaces.
var skuLike = regexp.MustCompile(`^[A-Z0-9_\-]{4,}$`)

// imageExt are the extensions we consider filename-y when a whole alt
// attribute ends in one of them.
var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true}

func runImageAltQuality(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaImageAltQuality)
	var issues []audit.Issue
	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		p.Doc.Find("img[src]").Each(func(_ int, s *goquery.Selection) {
			src, _ := s.Attr("src")
			if !strings.Contains(src, "cdn.shopify.com") {
				return // skip non-product images (logos, etc.)
			}
			alt, _ := s.Attr("alt")
			alt = strings.TrimSpace(alt)
			if reason := badAlt(alt); reason != "" {
				issues = append(issues, audit.Issue{
					URL:          p.URL,
					ProductTitle: productTitle(p),
					Detail:       "img alt is " + reason,
					Evidence:     truncate(src, 120),
				})
			}
		})
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

// badAlt returns a non-empty string describing why the alt is unsuitable, or "".
func badAlt(alt string) string {
	if alt == "" {
		return "empty"
	}
	low := strings.ToLower(alt)
	if imageExts[strings.ToLower(path.Ext(low))] {
		return "a filename: " + alt
	}
	if skuLike.MatchString(alt) {
		return "a SKU-like code: " + alt
	}
	if len(alt) < 5 {
		return "too short: " + alt
	}
	return ""
}
