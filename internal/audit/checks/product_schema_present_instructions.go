package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsProductSchemaPresent() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Product pages aren't emitting any JSON-LD structured data. Without it Google " +
			"can't reliably parse price, availability, brand, or image — your products will be " +
			"disapproved or omitted from rich results.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "20–40 min once per theme",
		Steps: []audit.Step{
			{Number: 1, Action: "Check whether your theme already supports structured data; modern Dawn-based themes do.",
				Path: "Shopify admin → Online Store → Themes → Customize → search 'Schema'"},
			{Number: 2, Action: "If your theme is older or heavily customized, install Shopify's free Search & Discovery app.",
				Path: "Shopify admin → Apps → Add Search & Discovery"},
			{Number: 3, Action: "Alternatively edit theme/templates/product.liquid (or product.json + sections/main-product.liquid on Online Store 2.0) to render a JSON-LD block.",
				Path:   "Shopify admin → Online Store → Themes → … → Edit code → Sections/main-product.liquid",
				Detail: `Use {{ product | structured_data }} on Liquid 2.0, or hand-write a <script type="application/ld+json"> block emitting Product, Offer, Brand, and image[].`},
			{Number: 4, Action: "Re-test the product URL with Google's Rich Results Test.",
				Path: "https://search.google.com/test/rich-results"},
		},
		DocsURL:      "https://shopify.dev/docs/storefronts/themes/seo/structured-data",
		WhyItMatters: "Without structured data Google falls back to scraping the rendered HTML and frequently mis-detects price and availability, causing GMC disapprovals. Rich Results (price, stock, ratings) also disappear from organic search.",
	}
}
