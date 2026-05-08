package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsShippingPolicyLinked() audit.FixInstructions {
	return audit.FixInstructions{
		Summary:      "Your shipping policy isn't linked from the homepage. GMC requires a clearly-discoverable shipping/delivery policy and may disapprove your products until it's published.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "5–10 min",
		Steps: []audit.Step{
			{Number: 1, Action: "Generate a shipping policy from Shopify's template if you don't have one.",
				Path: "Shopify admin → Settings → Policies → Shipping policy → Create from template"},
			{Number: 2, Action: "Add real numbers: which carriers, transit times, fees, and which countries you ship to.",
				Path: "Shopify admin → Settings → Policies → Shipping policy"},
			{Number: 3, Action: "Save and confirm Shopify exposes it at /policies/shipping-policy.",
				Path: "https://{shop}/policies/shipping-policy"},
			{Number: 4, Action: "Add the link to your footer menu.",
				Path: "Shopify admin → Online Store → Navigation → Footer menu → Add menu item"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/your-account/policies",
		WhyItMatters: "Buyers need shipping information before they buy. GMC and most consumer-protection regulations (US FTC mail/internet rule, EU CRD, UK CCRs) require a published shipping policy.",
	}
}
