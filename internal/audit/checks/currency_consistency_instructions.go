package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsCurrencyConsistency() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Different products are rendering with different currencies on the same storefront. GMC " +
			"expects one currency per feed/render; mixed currencies break Shopping ads and disable price-comparison surfaces.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "15–30 min",
		Steps: []audit.Step{
			{Number: 1, Action: "Confirm your store's primary currency.",
				Path: "Shopify admin → Settings → Store details → Store currency"},
			{Number: 2, Action: "If you sell internationally with Shopify Markets, each market has its own currency. The audit only sees one render — consider running per-market audits.",
				Path: "Shopify admin → Settings → Markets"},
			{Number: 3, Action: "Audit any custom theme code that hardcodes a currency in JSON-LD; switch to the Liquid {{ shop.currency }} or {{ cart.currency.iso_code }} variables.",
				Path: "Shopify admin → Online Store → Themes → … → Edit code"},
			{Number: 4, Action: "If the wrong currency is on a single product, it's almost always a metafield override — clear it.",
				Path: "Shopify admin → Products → {product name} → Metafields"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/6324454",
		WhyItMatters: "GMC requires consistent currency per feed. A single product in a different currency suspends ads for the affected SKUs and can trigger a manual review of the entire account.",
	}
}
