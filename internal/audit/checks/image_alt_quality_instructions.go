package checks

import "github.com/example/gmcauditor/internal/audit"

func instructionsImageAltQuality() audit.FixInstructions {
	return audit.FixInstructions{
		Summary: "Product images have unhelpful alt text — empty, a filename like \"IMG_4521.jpg\", or a SKU like \"WB-32-BLK\". " +
			"That's bad for accessibility, bad for SEO, and means GMC can't use the alt text as a fallback when the image fails to load.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "1–2 min per image",
		Steps: []audit.Step{
			{Number: 1, Action: "Open the failing product and edit the alt text on each image.",
				Path: "Shopify admin → Products → {product name} → Media → click image → Edit alt text"},
			{Number: 2, Action: "Write a short, accurate description: what the image shows, not the filename.",
				Detail: "Good: \"Stainless steel water bottle, black, 32oz, with carry handle\". Bad: \"WB-32-BLK\", \"IMG_4521.jpg\", \"image\"."},
			{Number: 3, Action: "If you have many images, the Bulk editor can edit alt text for many products at once.",
				Path: "Shopify admin → Products → Bulk edit → Add fields → Image alt"},
		},
		DocsURL:      "https://help.shopify.com/en/manual/online-store/themes/customizing-themes/edit-image-alt-text",
		WhyItMatters: "Image alt text is required by accessibility law (ADA, EAA) and is a top-tier ranking signal for Google Image Search. Filenames as alt text break screen readers and waste a free SEO slot.",
	}
}
