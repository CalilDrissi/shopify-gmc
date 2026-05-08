package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsProductImagesPresent() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Some products have no images. GMC disapproves products without an image; Shopping ads can't render and free listings won't show.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "2–5 min per product",
		Steps: []audit.Step{
			{Number: 1, Action: "Take or source a photo of the actual product (no logos, no \"coming soon\" placeholders).",
				Detail: "GMC's image policy requires a photo of the product itself. Stock images are allowed only if accurate."},
			{Number: 2, Action: "Upload to the product's Media section. Use square 1:1 PNG or JPG, ≥800×800 ideally.",
				Path: "Shopify admin → Products → {product name} → Media → Add files"},
			{Number: 3, Action: "Confirm the image appears in your storefront and in product schema.",
				Path: "https://{shop}/products/{handle}"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/6324350",
		WhyItMatters: "An image is a hard requirement for GMC product listings. No image = product disapproved = no Shopping ads + no free listings + flagged in Search Console.",
	}
}
