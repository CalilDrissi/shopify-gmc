package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsProhibitedContentSignals() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "We spotted language that often correlates with restricted product categories on Google Merchant Center: " +
			"CBD/cannabis, weapons/ammunition, or unverified health claims. This is a heads-up, not a verdict — please review with a human.",
		Difficulty:   audit.DifficultyTechnical,
		TimeEstimate: "varies — compliance review",
		Steps: []audit.Step{
			{Number: 1, Action: "Review each flagged passage. Many false positives — \"reverse coil\" can match \"reverse\".",
				Detail: "Check the Evidence text in each issue."},
			{Number: 2, Action: "If the product genuinely falls in a restricted category, GMC has a separate approval process for it.",
				Detail: "CBD requires an Eligible Country list + LegitScript / GW Pharma certifications. Health claims must be substantiated under FTC guidelines. Firearms have country-by-country rules."},
			{Number: 3, Action: "If the language is marketing copy that overstates benefits, rewrite to factual claims with citations.",
				Detail: "Replace \"clinically proven\" with the actual study citation, or remove if you don't have one."},
			{Number: 4, Action: "Document compliance decisions in your shared docs so legal/audit can find them later.",
				Detail: "This audit isn't a substitute for legal review."},
		},
		DocsURL:      "https://support.google.com/merchants/answer/6149970",
		WhyItMatters: "GMC suspensions in restricted categories are notoriously hard to undo and can take down your whole account, not just one product. Catching language early is much cheaper than appealing a suspension.",
	}
}
