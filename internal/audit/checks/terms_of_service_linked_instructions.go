package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsTermsOfServiceLinked() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Your terms of service / terms of use isn't linked from the homepage. This is the document " +
			"that governs disputes, refunds, liability caps, and venue selection. Have a lawyer draft it.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "10 min to publish + lawyer review",
		Steps: []audit.Step{
			{Number: 1, Action: "Have your lawyer draft or review the terms. Do not use AI-generated terms — they may be unenforceable in your jurisdiction.",
				Detail: "Pay particular attention to limitation of liability, indemnification, governing law, dispute resolution, and the warranty disclaimer."},
			{Number: 2, Action: "Paste the reviewed terms into Shopify's policy editor.",
				Path: "Shopify admin → Settings → Policies → Terms of service"},
			{Number: 3, Action: "Confirm Shopify exposes the page at /policies/terms-of-service.",
				Path: "https://{shop}/policies/terms-of-service"},
			{Number: 4, Action: "Add the link to your footer menu and to checkout.",
				Path: "Shopify admin → Online Store → Navigation → Footer menu"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/your-account/policies",
		WhyItMatters: "Terms of service is the contract you have with every buyer. Missing terms make it harder to defend chargebacks and lawsuits, and many ad networks (including GMC) require them as a baseline policy.",
	}
}
