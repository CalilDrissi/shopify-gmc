package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsAboutPageExists() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Your store has no About page (or it's too short to count as one). Without it visitors can't tell who's behind the store, and Google's quality reviewers downrank stores that look like dropshipping shells.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "20–30 min for a real first draft",
		Steps: []audit.Step{
			{Number: 1, Action: "Create a new page at /pages/about (or /pages/about-us).",
				Path: "Shopify admin → Online Store → Pages → Add page (handle: about)"},
			{Number: 2, Action: "Cover at minimum: who you are, why the brand exists, where products are sourced/made, and how to reach you.",
				Detail: "Aim for at least 300 characters of real prose. A photo of the founder or workspace helps."},
			{Number: 3, Action: "Add the page to the footer menu.",
				Path: "Shopify admin → Online Store → Navigation → Footer menu → Add menu item → /pages/about"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/online-store/themes/managing-themes/sections-blocks/about-us",
		WhyItMatters: "An About page is one of the strongest trust signals on a small commerce site. GMC's manual reviewers explicitly call out missing brand backstory as a quality concern; ad accounts can be paused over it.",
	}
}
