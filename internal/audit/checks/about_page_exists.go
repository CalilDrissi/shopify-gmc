package checks

import (
	"context"
	"strings"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaAboutPageExists,
		Run:          runAboutPageExists,
		Instructions: instructionsAboutPageExists,
	})
}

var metaAboutPageExists = audit.Meta{
	ID:              "about_page_exists",
	Title:           "About page exists with real, substantive content",
	Category:        "trust",
	DefaultSeverity: audit.SeverityWarning,
	AIFixEligible:   true,
}

const aboutMinChars = 300

func runAboutPageExists(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaAboutPageExists)
	page := cx.PolicyPages["about"]
	if page == nil {
		page = cx.PolicyPages["about-us"]
	}
	if page == nil || page.Doc == nil {
		return audit.FinishFailed(r, []audit.Issue{{
			URL:    cx.StoreURL + "/pages/about",
			Detail: "No /pages/about (or /pages/about-us) detected, and the homepage doesn't link to one.",
		}})
	}
	text := strings.Join(strings.Fields(extractText(page.Doc)), " ") // collapse whitespace
	if len(text) < aboutMinChars {
		return audit.FinishFailed(r, []audit.Issue{{
			URL:    page.URL,
			Detail: "About page exists but is too short to be substantive (got " +
				itoaInt(len(text)) + " chars, need ≥" + itoaInt(aboutMinChars) + ").",
			Evidence: truncate(text, 200),
		}})
	}
	return audit.FinishPassed(r)
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
