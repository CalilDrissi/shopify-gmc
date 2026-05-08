package checks

import (
	"context"
	"strings"
	"unicode"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaProductTitleQuality,
		Run:          runProductTitleQuality,
		Instructions: instructionsProductTitleQuality,
	})
}

var metaProductTitleQuality = audit.Meta{
	ID:              "product_title_quality",
	Title:           "Product titles are clean (no ALL CAPS, no excessive punctuation, <150 chars)",
	Category:        "content",
	DefaultSeverity: audit.SeverityWarning,
	AIFixEligible:   true,
}

const (
	titleMaxChars         = 150
	titleMaxExclamations  = 2
	titleMaxAllCapsRatio  = 0.6
	titleMinLettersForCap = 8 // skip the all-caps test for very short titles ("USB-C")
)

func runProductTitleQuality(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaProductTitleQuality)
	var issues []audit.Issue
	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		title := productTitle(p)
		if title == "" {
			continue
		}
		if reasons := titleProblems(title); len(reasons) > 0 {
			issues = append(issues, audit.Issue{
				URL:          p.URL,
				ProductTitle: title,
				Detail:       "Title issues: " + strings.Join(reasons, "; "),
				Evidence:     title,
			})
		}
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

// productTitle returns the best title we can find: schema:name → og:title →
// <h1> → <title>.
func productTitle(p *audit.Page) string {
	if product, ok := extractProductSchema(p.Doc); ok {
		if t := stringField(product, "name"); t != "" {
			return strings.TrimSpace(t)
		}
	}
	if og, ok := p.Doc.Find(`meta[property="og:title"]`).First().Attr("content"); ok && og != "" {
		return strings.TrimSpace(og)
	}
	if h1 := strings.TrimSpace(p.Doc.Find("h1").First().Text()); h1 != "" {
		return h1
	}
	return strings.TrimSpace(p.Doc.Find("title").First().Text())
}

func titleProblems(title string) []string {
	var reasons []string
	if len(title) > titleMaxChars {
		reasons = append(reasons, "longer than "+itoaInt(titleMaxChars)+" chars")
	}
	if exclam := strings.Count(title, "!"); exclam > titleMaxExclamations {
		reasons = append(reasons, "uses "+itoaInt(exclam)+" exclamation marks")
	}
	if isShoutyTitle(title) {
		reasons = append(reasons, "ALL CAPS shout-text")
	}
	return reasons
}

// isShoutyTitle returns true if the title is dominated by uppercase letters.
// We require at least titleMinLettersForCap letters so acronyms like "USB-C
// Cable" don't trip the heuristic.
func isShoutyTitle(title string) bool {
	letters, upper := 0, 0
	for _, r := range title {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	if letters < titleMinLettersForCap {
		return false
	}
	return float64(upper)/float64(letters) >= titleMaxAllCapsRatio
}
