package checks

import (
	"context"
	"sort"
	"strings"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaCurrencyConsistency,
		Run:          runCurrencyConsistency,
		Instructions: instructionsCurrencyConsistency,
	})
}

var metaCurrencyConsistency = audit.Meta{
	ID:              "currency_consistency",
	Title:           "All product prices use the same currency in the rendered storefront",
	Category:        "structured-data",
	DefaultSeverity: audit.SeverityError,
	AIFixEligible:   true,
}

func runCurrencyConsistency(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaCurrencyConsistency)
	currencies := map[string][]string{} // currency → list of URLs
	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		product, ok := extractProductSchema(p.Doc)
		if !ok {
			continue
		}
		ccy := extractCurrency(product["offers"])
		if ccy == "" {
			continue
		}
		ccy = strings.ToUpper(ccy)
		currencies[ccy] = append(currencies[ccy], p.URL)
	}
	if len(currencies) <= 1 {
		return audit.FinishPassed(r)
	}

	// Sort by number of products desc — the most-used currency is the
	// "expected" one, the others are the outliers.
	type kv struct {
		ccy   string
		count int
	}
	var sorted []kv
	for c, urls := range currencies {
		sorted = append(sorted, kv{c, len(urls)})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
	expected := sorted[0].ccy

	var issues []audit.Issue
	for _, kv := range sorted[1:] {
		for _, u := range currencies[kv.ccy] {
			issues = append(issues, audit.Issue{
				URL: u,
				Detail: "priceCurrency=" + kv.ccy + " but most products use " + expected +
					". Mixed currencies in the same render confuse GMC and break Shopping ads.",
			})
		}
	}
	return audit.FinishFailed(r, issues)
}

func extractCurrency(v any) string {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x["priceCurrency"].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
		if inner, ok := x["offers"]; ok {
			return extractCurrency(inner)
		}
	case []any:
		for _, e := range x {
			if c := extractCurrency(e); c != "" {
				return c
			}
		}
	}
	return ""
}
