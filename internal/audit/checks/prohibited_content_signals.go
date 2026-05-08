package checks

import (
	"context"
	"regexp"
	"strings"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaProhibitedContentSignals,
		Run:          runProhibitedContentSignals,
		Instructions: instructionsProhibitedContentSignals,
	})
}

var metaProhibitedContentSignals = audit.Meta{
	ID:              "prohibited_content_signals",
	Title:           "Heuristics: signals of CBD, weapons, or unverified health claims",
	Category:        "compliance",
	DefaultSeverity: audit.SeverityWarning,
	AIFixEligible:   false, // legal/compliance review — not a copy fix
}

// signalCategories maps a category label to the regex that matches plausible
// trigger language. We deliberately keep these conservative — false positives
// on this check are noisy but harmless; false negatives are dangerous.
var signalCategories = map[string]*regexp.Regexp{
	"cbd_or_cannabis": regexp.MustCompile(`(?i)\b(cbd|cannabis|thc|delta-?[89]|hemp\s+(extract|oil|gummies)|kratom)\b`),
	"weapons":         regexp.MustCompile(`(?i)\b(firearm|handgun|rifle|shotgun|ammunition|silencer|suppressor|magazine\s+capacity|bump\s+stock)\b`),
	"health_claims":   regexp.MustCompile(`(?i)\b(cure[sd]?|treats?|prevent[s]?|fda[\-\s]approved|clinically\s+proven|miracle|reverse\s+(diabetes|cancer|aging))\b`),
}

func runProhibitedContentSignals(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaProhibitedContentSignals)
	var issues []audit.Issue

	scan := func(label, text, urlOrTitle string) {
		for category, re := range signalCategories {
			if loc := re.FindStringIndex(text); loc != nil {
				issues = append(issues, audit.Issue{
					URL:          label,
					ProductTitle: urlOrTitle,
					Detail:       "Potential " + category + " signal — manual compliance review recommended.",
					Evidence:     evidenceWindow(text, loc[0], loc[1]),
				})
			}
		}
	}

	if cx.Homepage != nil && cx.Homepage.Doc != nil {
		scan(cx.Homepage.URL, extractText(cx.Homepage.Doc), "homepage")
	}
	for _, p := range cx.ProductPages {
		if p == nil || p.Doc == nil {
			continue
		}
		scan(p.URL, extractText(p.Doc), productTitle(p))
	}

	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	// Surface as INFO (not fail): this check is a flagger, never a blocker.
	return audit.FinishInfo(r, issues)
}

func evidenceWindow(text string, start, end int) string {
	if start < 0 || end > len(text) || start >= end {
		return ""
	}
	const ctx = 60
	from := start - ctx
	if from < 0 {
		from = 0
	}
	to := end + ctx
	if to > len(text) {
		to = len(text)
	}
	out := strings.TrimSpace(strings.ReplaceAll(text[from:to], "\n", " "))
	return truncate(out, 240)
}
