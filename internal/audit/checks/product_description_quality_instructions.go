package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsProductDescriptionQuality() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Some product descriptions are too short, identical to other products, or contain raw HTML " +
			"artifacts (&nbsp;, <br>, <p>) that didn't render. GMC penalises thin and duplicate copy.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "5–10 min per product",
		Steps: []audit.Step{
			{Number: 1, Action: "Open the failing product and rewrite the description to at least 150 characters of unique copy.",
				Path: "Shopify admin → Products → {product name} → Description"},
			{Number: 2, Action: "Cover at minimum: what it is, key materials/specs, who it's for, what's in the box. Avoid the same boilerplate paragraph across multiple products.",
				Detail: "Per-variant copy that says \"Available in 5 colors\" repeated for each of those 5 SKUs is what triggers the duplicate-description warning."},
			{Number: 3, Action: "If the description shows literal HTML (\"&lt;br&gt;\" or \"&nbsp;\"), the source was pasted as text instead of rich text.",
				Path: "Shopify admin → Products → {product name} → Description → Toggle HTML editor → paste cleanly"},
			{Number: 4, Action: "Re-save and re-run the audit.",
				Path: "shopifygmc → Stores → {store} → Run audit"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/6324468",
		WhyItMatters: "Description is one of the strongest ranking signals for Shopping. Thin or duplicated copy across SKUs causes Google to choose a single \"canonical\" listing per group and ignore the rest.",
	}
}
