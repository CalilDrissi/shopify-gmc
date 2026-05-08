package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsHTTPSEverywhere() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Some resources on your storefront load over plain HTTP. Browsers block mixed content; " +
			"Google deprioritises non-HTTPS pages and GMC will disapprove products with mixed-content checkout pages.",
		Difficulty:   audit.DifficultyTechnical,
		TimeEstimate: "30–90 min depending on theme age",
		Steps: []audit.Step{
			{Number: 1, Action: "Confirm your store domain is on HTTPS. Shopify provisions free TLS automatically — if it's not active, the cert may be still propagating.",
				Path: "Shopify admin → Settings → Domains"},
			{Number: 2, Action: "Search your theme code for `http://` literals and update them to `https://` or protocol-relative `//`.",
				Path:   "Shopify admin → Online Store → Themes → … → Edit code",
				Detail: "Common offenders: hardcoded image URLs, custom font CDNs, embedded videos, third-party scripts."},
			{Number: 3, Action: "Re-upload any product image whose src starts with http:// — they'll be rewritten to https:// automatically when re-uploaded.",
				Path: "Shopify admin → Products → {product} → Media"},
			{Number: 4, Action: "Open the affected page in Chrome DevTools → Console and verify no \"Mixed Content\" warnings remain.",
				Path: "Chrome → ⋯ → More tools → Developer tools → Console"},
		},
		DocsURL: "https://web.dev/articles/fixing-mixed-content",
		WhyItMatters: "Mixed content is blocked by default in modern browsers, breaking the page silently for visitors. GMC requires checkout and product pages to be served over HTTPS without insecure subresources.",
	}
}
