package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsPrivacyPolicyLinked() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Your privacy policy isn't linked from the homepage. GDPR, CCPA, CalOPPA, and most other " +
			"privacy regimes require a conspicuous link on every page that collects data. Have a lawyer review the text.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "10 min to publish + lawyer review",
		Steps: []audit.Step{
			{Number: 1, Action: "Have your lawyer prepare or review the privacy policy. Do not auto-generate or AI-generate this text — it carries legal liability.",
				Detail: "Shopify's policy generator is a starting draft only; it cannot account for your data flows, sub-processors, ad tracking, or cookie usage."},
			{Number: 2, Action: "Paste the reviewed policy into Shopify's policy editor.",
				Path: "Shopify admin → Settings → Policies → Privacy policy"},
			{Number: 3, Action: "Confirm Shopify exposes it at /policies/privacy-policy.",
				Path: "https://{shop}/policies/privacy-policy"},
			{Number: 4, Action: "Add the link to your footer menu (and a cookie banner if you serve EU/UK/CA visitors).",
				Path: "Shopify admin → Online Store → Navigation → Footer menu"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/your-account/policies",
		WhyItMatters: "Privacy compliance is a legal requirement, not a UX nicety. Missing or hidden privacy policies expose you to fines under GDPR (up to 4% of revenue) and CCPA, and expose you to FTC actions in the US. GMC will also disapprove products until a policy is visible.",
	}
}
