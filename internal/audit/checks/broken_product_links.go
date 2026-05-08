package checks

import (
	"context"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaBrokenProductLinks,
		Run:          runBrokenProductLinks,
		Instructions: instructionsBrokenProductLinks,
	})
}

var metaBrokenProductLinks = audit.Meta{
	ID:              "broken_product_links",
	Title:           "Product / collection / policy pages all returned 200",
	Category:        "infra",
	DefaultSeverity: audit.SeverityError,
	AIFixEligible:   true,
}

func runBrokenProductLinks(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaBrokenProductLinks)
	var issues []audit.Issue

	check := func(label string, p *audit.Page) {
		if p == nil {
			return
		}
		if p.FetchError != nil {
			issues = append(issues, audit.Issue{
				URL:    p.URL,
				Detail: label + " fetch failed: " + p.FetchError.Error(),
			})
			return
		}
		if p.StatusCode >= 400 {
			issues = append(issues, audit.Issue{
				URL:    p.URL,
				Detail: label + " returned HTTP " + itoaInt(p.StatusCode),
			})
		}
	}

	check("Homepage", cx.Homepage)
	for _, p := range cx.ProductPages {
		check("Product", p)
	}
	for _, p := range cx.CollectionPages {
		check("Collection", p)
	}
	for slug, p := range cx.PolicyPages {
		check("Policy ("+slug+")", p)
	}

	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}
