package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaProductSchemaComplete,
		Run:          runProductSchemaComplete,
		Instructions: instructionsProductSchemaComplete,
	})
}

var metaProductSchemaComplete = audit.Meta{
	ID:              "product_schema_complete",
	Title:           "Product structured data has all GMC-required fields",
	Category:        "structured-data",
	DefaultSeverity: audit.SeverityError,
	AIFixEligible:   true,
}

// requiredProductFields is the GMC-required subset of schema.org/Product.
// Reference: https://developers.google.com/search/docs/appearance/structured-data/product
var requiredProductFields = []string{
	"name",
	"image",
	"description",
	"offers.price",
	"offers.priceCurrency",
	"offers.availability",
	"brand",
}

func runProductSchemaComplete(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaProductSchemaComplete)
	var issues []audit.Issue

	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		product, ok := extractProductSchema(p.Doc)
		if !ok {
			// "schema present" is a separate check; here we only report on
			// products that DO have a schema but are missing required fields.
			continue
		}
		missing := missingRequiredFields(product)
		if len(missing) == 0 {
			continue
		}
		title := stringField(product, "name")
		issues = append(issues, audit.Issue{
			URL:          p.URL,
			ProductTitle: title,
			Detail:       fmt.Sprintf("Missing required fields: %s", strings.Join(missing, ", ")),
			Evidence:     truncate(p.HTML, 220),
		})
	}

	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

// ----------------------------------------------------------------------------
// JSON-LD extraction.
// ----------------------------------------------------------------------------

// extractProductSchema returns the first JSON-LD Product object on the page.
// JSON-LD blocks may be a single object, an array, or have @graph; we walk
// each shape until we find @type == "Product".
func extractProductSchema(doc *goquery.Document) (map[string]any, bool) {
	var found map[string]any
	doc.Find(`script[type="application/ld+json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			return true
		}
		// Try as single object first.
		var single map[string]any
		if err := json.Unmarshal([]byte(raw), &single); err == nil {
			if p := pickProduct(single); p != nil {
				found = p
				return false
			}
		}
		// Try as array of objects.
		var arr []map[string]any
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			for _, obj := range arr {
				if p := pickProduct(obj); p != nil {
					found = p
					return false
				}
			}
		}
		return true
	})
	return found, found != nil
}

func pickProduct(obj map[string]any) map[string]any {
	if isProduct(obj) {
		return obj
	}
	// @graph is a JSON-LD container; walk its entries.
	if g, ok := obj["@graph"].([]any); ok {
		for _, entry := range g {
			if m, ok := entry.(map[string]any); ok && isProduct(m) {
				return m
			}
		}
	}
	return nil
}

func isProduct(obj map[string]any) bool {
	t, ok := obj["@type"]
	if !ok {
		return false
	}
	switch v := t.(type) {
	case string:
		return v == "Product"
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s == "Product" {
				return true
			}
		}
	}
	return false
}

// ----------------------------------------------------------------------------
// Required-field detection.
// ----------------------------------------------------------------------------

func missingRequiredFields(p map[string]any) []string {
	var missing []string
	for _, path := range requiredProductFields {
		if !hasField(p, path) {
			missing = append(missing, path)
		}
	}
	return missing
}

func hasField(p map[string]any, path string) bool {
	switch path {
	case "name":
		return nonEmptyString(p["name"])
	case "image":
		return imageFieldOK(p["image"])
	case "description":
		return nonEmptyString(p["description"])
	case "brand":
		return brandFieldOK(p["brand"])
	case "offers.price":
		return offerHas(p["offers"], "price")
	case "offers.priceCurrency":
		return offerHas(p["offers"], "priceCurrency")
	case "offers.availability":
		return offerHas(p["offers"], "availability")
	}
	return false
}

func nonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}

func imageFieldOK(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
			if m, ok := e.(map[string]any); ok && nonEmptyString(m["url"]) {
				return true
			}
		}
	case map[string]any:
		return nonEmptyString(x["url"])
	}
	return false
}

func brandFieldOK(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case map[string]any:
		return nonEmptyString(x["name"])
	}
	return false
}

// offerHas walks Offer or AggregateOffer (object or array) and returns true if
// any entry has a non-empty value at the given key.
func offerHas(v any, key string) bool {
	switch x := v.(type) {
	case map[string]any:
		if nonEmptyAny(x[key]) {
			return true
		}
		// AggregateOffer carries low/high prices in offers[].priceCurrency etc.
		if inner, ok := x["offers"]; ok {
			return offerHas(inner, key)
		}
	case []any:
		for _, e := range x {
			if offerHas(e, key) {
				return true
			}
		}
	}
	return false
}

func nonEmptyAny(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case float64:
		return true
	case int:
		return true
	case bool:
		return true
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	}
	return v != nil
}

func stringField(p map[string]any, key string) string {
	if s, ok := p[key].(string); ok {
		return s
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
