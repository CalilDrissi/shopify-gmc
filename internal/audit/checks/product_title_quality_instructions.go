package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsProductTitleQuality() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Some product titles violate Google Merchant Center title quality rules. Common offenders: " +
			"ALL CAPS, three-or-more exclamation marks, or titles longer than 150 characters.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "1–3 min per product",
		Steps: []audit.Step{
			{Number: 1, Action: "Open the failing product and rewrite the title using sentence case or title case (not ALL CAPS).",
				Path: "Shopify admin → Products → {product name} → Title"},
			{Number: 2, Action: "Remove decorative punctuation. \"BEST DEAL!!! 🔥\" should become \"Wireless Charging Pad — 15W Fast Charge\".",
				Detail: "Title format that converts well: {Brand} {Product Type} – {Key Attribute} ({Variant})."},
			{Number: 3, Action: "Keep titles under 150 characters. Google truncates around ~70 chars in Shopping ads anyway.",
				Path: "Shopify admin → Products → {product name} → Title"},
			{Number: 4, Action: "Re-save and re-run the audit.",
				Path: "shopifygmc → Stores → {store} → Run audit"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/6324415",
		WhyItMatters: "Title quality directly affects GMC ad rank and click-through. ALL CAPS and excessive punctuation also trigger the \"promotional language\" disapproval reason in GMC and can suspend the entire account.",
	}
}
