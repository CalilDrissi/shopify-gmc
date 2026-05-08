package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsProductSchemaComplete() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Your product pages have JSON-LD structured data, but it's missing fields that " +
			"Google Merchant Center requires before the listing is approved.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "30–45 min for the first product, then 1–2 min per product",
		Steps: []audit.Step{
			{
				Number: 1,
				Action: "Open the failing product in your Shopify admin and confirm what data is missing on the page.",
				Path:   "Shopify admin → Products → {product name}",
			},
			{
				Number: 2,
				Action: "Set the brand. Shopify's default Product schema falls back to your store name; if that's not the actual manufacturer, set it explicitly.",
				Path:   "Shopify admin → Products → {product name} → Vendor",
				Detail: "Vendor is what most Shopify themes serialize as schema:brand. Use the actual manufacturer name (\"Acme Corp\"), not the store name.",
			},
			{
				Number: 3,
				Action: "Make sure the product has at least one image of the actual product (no placeholders, no logos).",
				Path:   "Shopify admin → Products → {product name} → Media",
			},
			{
				Number: 4,
				Action: "Write a description ≥150 characters. Describe what the product is and the key attributes a buyer cares about.",
				Path:   "Shopify admin → Products → {product name} → Description",
			},
			{
				Number: 5,
				Action: "Confirm price and currency are set. Shopify automatically emits offers.price + offers.priceCurrency from this field.",
				Path:   "Shopify admin → Products → {product name} → Pricing",
			},
			{
				Number: 6,
				Action: "Confirm inventory is tracked so offers.availability resolves to InStock or OutOfStock.",
				Path:   "Shopify admin → Products → {product name} → Inventory → Track quantity",
				Detail: "If you don't track inventory, edit your theme's product-template.liquid to hardcode availability=https://schema.org/InStock when the product is published.",
			},
			{
				Number: 7,
				Action: "If your theme doesn't emit a complete Product schema, install Shopify's free Search & Discovery app or use a JSON-LD theme app like Schema Plus.",
				Path:   "Shopify admin → Apps → Add Search & Discovery",
				Detail: "Most modern Dawn-based themes already emit a full Product schema; older custom themes may not.",
			},
			{
				Number: 8,
				Action: "Re-test using Google's Rich Results Test on the product URL.",
				Path:   "https://search.google.com/test/rich-results",
			},
		},
		DocsURL: "https://developers.google.com/search/docs/appearance/structured-data/product",
		WhyItMatters: "Google Merchant Center pulls product attributes (price, availability, brand, GTIN) " +
			"from your structured data when it can't read them from the feed. Missing required fields cause " +
			"listings to be disapproved, products to be excluded from free listings and Performance Max ads, " +
			"and rich snippets to disappear from Search.",
	}
}
