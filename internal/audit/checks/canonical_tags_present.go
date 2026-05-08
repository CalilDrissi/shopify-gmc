package checks

import (
	"context"
	"strings"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaCanonicalTagsPresent,
		Run:          runCanonicalTagsPresent,
		Instructions: instructionsCanonicalTagsPresent,
	})
}

var metaCanonicalTagsPresent = audit.Meta{
	ID:              "canonical_tags_present",
	Title:           "Product pages declare a canonical URL",
	Category:        "infra",
	DefaultSeverity: audit.SeverityWarning,
	AIFixEligible:   true,
}

func runCanonicalTagsPresent(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaCanonicalTagsPresent)
	var issues []audit.Issue
	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		href, _ := p.Doc.Find(`link[rel="canonical"]`).First().Attr("href")
		href = strings.TrimSpace(href)
		if href == "" {
			issues = append(issues, audit.Issue{
				URL:    p.URL,
				Detail: `Missing <link rel="canonical">.`,
			})
			continue
		}
		// Canonical pointing to itself is correct; pointing to homepage or
		// to a different product is a sign of a misconfigured theme.
		lower := strings.ToLower(href)
		if lower == strings.ToLower(cx.StoreURL) || lower == strings.ToLower(cx.StoreURL)+"/" {
			issues = append(issues, audit.Issue{
				URL:      p.URL,
				Detail:   "Canonical points to the store homepage instead of the product itself.",
				Evidence: href,
			})
		}
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}
