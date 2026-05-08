package checks

import (
	"context"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaRefundPolicyLinked,
		Run:          runRefundPolicyLinked,
		Instructions: instructionsRefundPolicyLinked,
	})
}

var metaRefundPolicyLinked = audit.Meta{
	ID:              "refund_policy_linked",
	Title:           "Refund / return policy linked from the homepage",
	Category:        "policy",
	DefaultSeverity: audit.SeverityError,
	AIFixEligible:   true,
}

// refundLinkSignals matches anchor text or href fragments that indicate
// a refund or returns policy link.
var refundLinkSignals = []string{
	"refund", "returns", "return policy", "return-policy",
	"/policies/refund-policy", "/policies/return-policy",
}

func runRefundPolicyLinked(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaRefundPolicyLinked)
	if cx.Homepage == nil || cx.Homepage.Doc == nil {
		return audit.FinishFailed(r, []audit.Issue{{Detail: "homepage was not fetched"}})
	}
	if linkExists(cx.Homepage.Doc, refundLinkSignals) {
		return audit.FinishPassed(r)
	}
	if _, ok := cx.PolicyPages["refund-policy"]; ok {
		// Page exists at the canonical URL but isn't linked from the homepage.
		return audit.FinishFailed(r, []audit.Issue{{
			URL: cx.StoreURL,
			Detail: "Refund policy exists at /policies/refund-policy but isn't linked from the homepage. " +
				"GMC and most jurisdictions require it to be discoverable in one click.",
		}})
	}
	return audit.FinishFailed(r, []audit.Issue{{
		URL:    cx.StoreURL,
		Detail: "No refund / return policy detected. Add one and link it from the footer.",
	}})
}

// linkExists returns true if any <a> on the doc has an href or text that
// matches any of the given signal substrings (case-insensitive).
func linkExists(doc *goquery.Document, signals []string) bool {
	found := false
	doc.Find("a").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		href, _ := s.Attr("href")
		text := strings.TrimSpace(s.Text())
		hay := strings.ToLower(href + " " + text)
		for _, sig := range signals {
			if strings.Contains(hay, sig) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
