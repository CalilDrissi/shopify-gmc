package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsRobotsTxtValid() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Your robots.txt blocks Googlebot from URLs it needs to crawl. GMC relies on Google's index — " +
			"if products are blocked, listings are disapproved and ad campaigns can't serve.",
		Difficulty:   audit.DifficultyTechnical,
		TimeEstimate: "10–20 min",
		Steps: []audit.Step{
			{Number: 1, Action: "Shopify generates /robots.txt automatically. If you've overridden it via robots.txt.liquid, audit your custom rules.",
				Path: "Shopify admin → Online Store → Themes → … → Edit code → Templates/robots.txt.liquid"},
			{Number: 2, Action: "Remove or narrow Disallow rules that match /, /products/*, /collections/*, or /sitemap.xml for Googlebot.",
				Detail: "Common mistakes: copying a staging robots.txt with `User-agent: * / Disallow: /` into production, or blocking /products to hide drafts."},
			{Number: 3, Action: "Use Google Search Console's robots.txt Tester to confirm Googlebot can fetch /products/sample and /sitemap.xml.",
				Path: "https://search.google.com/search-console → URL Inspection"},
		},
		DocsURL:      "https://shopify.dev/docs/storefronts/themes/seo/edit-robots-txt-liquid",
		WhyItMatters: "If Google can't crawl your products it can't index them. GMC's free listings and Performance Max both consume Google's index, so a too-aggressive robots.txt silently disables both.",
	}
}
