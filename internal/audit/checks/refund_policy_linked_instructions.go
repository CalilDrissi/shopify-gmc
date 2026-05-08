package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsRefundPolicyLinked() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Your refund / return policy isn't linked from the homepage. Google Merchant Center " +
			"requires a clearly-discoverable returns policy, and Shopify's Standards & Disputes " +
			"team also flags missing policies on chargebacks.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "5–10 min",
		Steps: []audit.Step{
			{Number: 1, Action: "If you don't have a refund policy yet, generate one with Shopify's policy generator and edit to fit your business.",
				Path: "Shopify admin → Settings → Policies → Refund policy → Create from template"},
			{Number: 2, Action: "Make sure the policy is published. Once it is, Shopify exposes it at /policies/refund-policy.",
				Path: "Shopify admin → Settings → Policies → Refund policy → Save"},
			{Number: 3, Action: "Add the refund policy link to your footer menu so it appears on every page.",
				Path:   "Shopify admin → Online Store → Navigation → Footer menu → Add menu item",
				Detail: "Link target: Policies → Refund policy. Anchor text: \"Refund policy\" or \"Returns\"."},
			{Number: 4, Action: "Reload your homepage and confirm the link is visible in the footer.",
				Path: "Shopify admin → Online Store → Themes → View"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/your-account/policies",
		WhyItMatters: "GMC will disapprove product listings from a store with no visible refund policy. Most US states require a refund/return policy to be conspicuously posted; the EU's CRD and UK CCRs require it on consumer-facing pages.",
	}
}
