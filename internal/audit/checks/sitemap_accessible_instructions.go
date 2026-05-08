package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsSitemapAccessible() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "We couldn't fetch a usable sitemap. Shopify generates one automatically at /sitemap.xml; " +
			"if it's empty or unreachable, your products and collections may take much longer to be indexed.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "5–15 min",
		Steps: []audit.Step{
			{Number: 1, Action: "Visit /sitemap.xml on your store domain and confirm it returns XML (not 404 or HTML).",
				Path: "{store URL}/sitemap.xml"},
			{Number: 2, Action: "Make sure /sitemap.xml is referenced in robots.txt. Shopify does this by default; only check if you've overridden robots.txt.liquid.",
				Path: "Shopify admin → Online Store → Themes → … → Edit code → Templates/robots.txt.liquid"},
			{Number: 3, Action: "Submit the sitemap in Google Search Console so Google picks it up faster.",
				Path: "https://search.google.com/search-console → Sitemaps → Add"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/online-store/search-engine-optimization/finding-pages-on-your-site",
		WhyItMatters: "Without a sitemap, Google has to discover URLs by crawling links. Newly-published products may not appear in GMC for days; old retired products may stay listed too long.",
	}
}
