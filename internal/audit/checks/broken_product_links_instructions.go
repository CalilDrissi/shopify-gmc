package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsBrokenProductLinks() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "One or more pages we fetched returned an HTTP error or never responded. Broken product / policy links cause GMC disapprovals and waste ad spend by sending clicks to dead pages.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "5–20 min depending on count",
		Steps: []audit.Step{
			{Number: 1, Action: "Open the failing URL in a browser. Decide: should this product still exist?",
				Detail: "Common causes: handle was renamed, product was archived/unpublished, theme link points to an old slug."},
			{Number: 2, Action: "If the product is gone, set up a 301 redirect to the closest replacement product or its collection.",
				Path: "Shopify admin → Online Store → Navigation → URL redirects → Add"},
			{Number: 3, Action: "If the link is a typo in a theme, fix it in the theme code.",
				Path: "Shopify admin → Online Store → Themes → … → Edit code"},
			{Number: 4, Action: "Re-run the audit to confirm.",
				Path: "gmcauditor → Stores → {store} → Run audit"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/online-store/menus-and-links/url-redirect",
		WhyItMatters: "Every 404 from a Shopping ad is wasted spend and a quality penalty. GMC tracks link health and disapproves products that 404. Customer trust degrades fast on broken navigation.",
	}
}
