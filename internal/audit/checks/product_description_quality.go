package checks

import (
	"context"
	"strings"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaProductDescriptionQuality,
		Run:          runProductDescriptionQuality,
		Instructions: instructionsProductDescriptionQuality,
	})
}

var metaProductDescriptionQuality = audit.Meta{
	ID:              "product_description_quality",
	Title:           "Product descriptions are substantial, unique, and free of raw HTML",
	Category:        "content",
	DefaultSeverity: audit.SeverityWarning,
	AIFixEligible:   true,
}

const descriptionMinChars = 150

// htmlArtifacts is what we expect when a description was pasted from another
// site without HTML decoding — these slip into JSON-LD as plaintext.
var htmlArtifacts = []string{"&nbsp;", "&amp;amp;", "<br>", "<br/>", "<br />", "</p><p>", "&lt;br&gt;"}

func runProductDescriptionQuality(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaProductDescriptionQuality)
	var issues []audit.Issue
	descCounts := map[string]int{}
	descByPage := make([]string, 0, len(cx.ProductPages))

	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			descByPage = append(descByPage, "")
			continue
		}
		desc := productDescription(p)
		descByPage = append(descByPage, desc)
		descCounts[desc]++

		var reasons []string
		if len(desc) < descriptionMinChars {
			reasons = append(reasons, "shorter than "+itoaInt(descriptionMinChars)+" chars (got "+itoaInt(len(desc))+")")
		}
		for _, art := range htmlArtifacts {
			if strings.Contains(desc, art) {
				reasons = append(reasons, "contains raw HTML artifact: "+art)
				break
			}
		}
		if len(reasons) > 0 {
			issues = append(issues, audit.Issue{
				URL:          p.URL,
				ProductTitle: productTitle(p),
				Detail:       "Description issues: " + strings.Join(reasons, "; "),
				Evidence:     truncate(desc, 200),
			})
		}
	}

	// Second pass: surface duplicates across the catalog.
	for i, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		desc := descByPage[i]
		if desc == "" {
			continue
		}
		if descCounts[desc] > 1 {
			issues = append(issues, audit.Issue{
				URL:          p.URL,
				ProductTitle: productTitle(p),
				Detail: "Description is identical across " + itoaInt(descCounts[desc]) +
					" products — placeholder or copy-paste content. Each product needs its own copy.",
				Evidence: truncate(desc, 200),
			})
			descCounts[desc] = 0 // emit only once per duplicate group
		}
	}

	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

func productDescription(p *audit.Page) string {
	if product, ok := extractProductSchema(p.Doc); ok {
		if d := stringField(product, "description"); d != "" {
			return strings.TrimSpace(d)
		}
	}
	if og, ok := p.Doc.Find(`meta[property="og:description"]`).First().Attr("content"); ok && og != "" {
		return strings.TrimSpace(og)
	}
	if md, ok := p.Doc.Find(`meta[name="description"]`).First().Attr("content"); ok && md != "" {
		return strings.TrimSpace(md)
	}
	return ""
}
