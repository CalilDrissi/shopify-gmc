package checks

import (
	"context"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaProductSchemaPresent,
		Run:          runProductSchemaPresent,
		Instructions: instructionsProductSchemaPresent,
	})
}

var metaProductSchemaPresent = audit.Meta{
	ID:              "product_schema_present",
	Title:           "Product pages emit JSON-LD structured data",
	Category:        "structured-data",
	DefaultSeverity: audit.SeverityCritical,
	AIFixEligible:   true,
}

func runProductSchemaPresent(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaProductSchemaPresent)
	var issues []audit.Issue
	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		if _, ok := extractProductSchema(p.Doc); !ok {
			issues = append(issues, audit.Issue{
				URL:    p.URL,
				Detail: `No <script type="application/ld+json"> Product schema found on this product page.`,
			})
		}
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}
