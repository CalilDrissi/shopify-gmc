package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsProductIdentifierPresent() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Google Merchant Center requires every product to have a GTIN, MPN, or an explicit " +
			"identifier_exists=false declaration for handmade / private-label items. Without one, " +
			"product listings are disapproved.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "1–2 min per product",
		Steps: []audit.Step{
			{Number: 1, Action: "Open the failing product in Shopify admin.",
				Path: "Shopify admin → Products → {product name}"},
			{Number: 2, Action: "If the product has a manufacturer barcode (GTIN / EAN / UPC / ISBN), enter it in the Barcode field.",
				Path:   "Shopify admin → Products → {product name} → Variants → Inventory → Barcode",
				Detail: "Most themes emit this field as schema:gtin13 automatically."},
			{Number: 3, Action: "If the product has a manufacturer part number but no barcode, set the MPN via metafields.",
				Path:   "Shopify admin → Products → {product name} → Metafields → product.mpn",
				Detail: "Create the metafield definition under Settings → Custom data → Products if it doesn't exist."},
			{Number: 4, Action: "If the product is handmade or private label and genuinely has no manufacturer identifier, set identifier_exists=false.",
				Path:   "Shopify admin → Products → {product name} → Metafields → google.identifier_exists",
				Detail: "Set the metafield value to false (boolean). GMC accepts this as a valid response."},
			{Number: 5, Action: "Re-run the audit to confirm the fix.",
				Path: "shopifygmc → Stores → {store} → Run audit"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/6324461",
		WhyItMatters: "GMC matches your products to its catalog by GTIN. Listings without one are eligible for disapproval, get worse impressions, and are excluded from price-comparison surfaces like Shopping listings.",
	}
}
