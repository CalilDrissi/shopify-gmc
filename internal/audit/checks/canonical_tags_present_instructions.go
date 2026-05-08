package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsCanonicalTagsPresent() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: `Product pages should declare <link rel="canonical" href="..."> pointing at themselves. ` +
			"Without it, Google may treat duplicates (variant URLs, UTM-tagged links) as separate pages and " +
			"split ranking signals.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "10 min once for the theme",
		Steps: []audit.Step{
			{Number: 1, Action: "Most modern Shopify themes emit canonicals automatically via Liquid's {{ canonical_url }}.",
				Path: "Shopify admin → Online Store → Themes → … → Edit code → Layout/theme.liquid"},
			{Number: 2, Action: "If missing, add the tag to layout/theme.liquid inside the <head> block.",
				Detail: `<link rel="canonical" href="{{ canonical_url }}">`},
			{Number: 3, Action: "Open a product page and view source to confirm the canonical points at /products/{handle} (not the homepage).",
				Path: "Right click → View page source → search for rel=\"canonical\""},
		},
		DocsURL:      "https://shopify.dev/docs/storefronts/themes/architecture/templates/canonical-url",
		WhyItMatters: "Canonical tags consolidate ranking signals across duplicate URLs (variant params, query strings, UTM tags). Misdirected canonicals can de-index your products entirely.",
	}
}
