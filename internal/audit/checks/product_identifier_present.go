package checks

import (
	"context"
	"strings"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaProductIdentifierPresent,
		Run:          runProductIdentifierPresent,
		Instructions: instructionsProductIdentifierPresent,
	})
}

var metaProductIdentifierPresent = audit.Meta{
	ID:              "product_identifier_present",
	Title:           "Product has a unique identifier (GTIN / MPN) or identifier_exists=false",
	Category:        "structured-data",
	DefaultSeverity: audit.SeverityError,
	AIFixEligible:   true,
}

// gtinKeys are all the JSON-LD fields Shopify themes commonly emit for
// barcodes (GTIN-8/12/13/14, EAN, UPC, ISBN).
var gtinKeys = []string{"gtin", "gtin8", "gtin12", "gtin13", "gtin14", "ean", "upc", "isbn"}

func runProductIdentifierPresent(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaProductIdentifierPresent)
	var issues []audit.Issue
	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		product, ok := extractProductSchema(p.Doc)
		if !ok {
			continue
		}
		if hasIdentifier(product) {
			continue
		}
		issues = append(issues, audit.Issue{
			URL:          p.URL,
			ProductTitle: stringField(product, "name"),
			Detail: "No GTIN / MPN, and identifier_exists is not set to false. " +
				"Add a barcode in Shopify or set identifier_exists=false on this product.",
		})
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

func hasIdentifier(p map[string]any) bool {
	for _, k := range gtinKeys {
		if nonEmptyString(p[k]) {
			return true
		}
	}
	if nonEmptyString(p["mpn"]) {
		return true
	}
	if v, ok := p["identifier_exists"]; ok {
		switch x := v.(type) {
		case bool:
			return !x
		case string:
			return strings.EqualFold(x, "false")
		}
	}
	// Some themes nest under offers.gtin / offers.mpn
	if offerHas(p["offers"], "gtin13") || offerHas(p["offers"], "gtin") || offerHas(p["offers"], "mpn") {
		return true
	}
	return false
}
