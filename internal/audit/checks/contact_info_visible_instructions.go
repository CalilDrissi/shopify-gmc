package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsContactInfoVisible() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Buyers and Google can't find a phone number, email address, or postal address on your store. " +
			"GMC's quality criteria call this out, and visitors abandon stores that look anonymous.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "10 min",
		Steps: []audit.Step{
			{Number: 1, Action: "Decide which contact channel(s) you can actually staff: email, phone, contact form, or a chat widget.",
				Detail: "Don't list a number nobody answers — chargeback teams check this."},
			{Number: 2, Action: "Add a /pages/contact page with at minimum: email, business hours, and a contact form.",
				Path: "Shopify admin → Online Store → Pages → Add page (handle: contact)"},
			{Number: 3, Action: "Add the page to your footer menu.",
				Path: "Shopify admin → Online Store → Navigation → Footer menu → Add menu item → /pages/contact"},
			{Number: 4, Action: "If you have a brick-and-mortar address, also list it in the footer (small text, but text — not an image).",
				Path: "Shopify admin → Online Store → Themes → Customize → Footer"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/online-store/themes/managing-themes/sections-blocks/contact",
		WhyItMatters: "Visible contact info is a trust signal Google scores during quality reviews. It's also required by Shopify's terms of service for active stores. Without it, buyers can't recover lost orders and you can't defend chargebacks.",
	}
}
