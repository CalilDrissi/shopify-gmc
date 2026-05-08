package checks

import (
	"github.com/example/gmcauditor/internal/audit"
)

// runPolicyLinkCheck is the shared kernel for the four policy-link checks
// (refund / shipping / privacy / terms). Each caller supplies the canonical
// slug and the substring signals to scan for.
func runPolicyLinkCheck(meta audit.Meta, slug string, signals []string, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(meta)
	if cx.Homepage == nil || cx.Homepage.Doc == nil {
		return audit.FinishFailed(r, []audit.Issue{{Detail: "homepage was not fetched"}})
	}
	if linkExists(cx.Homepage.Doc, signals) {
		return audit.FinishPassed(r)
	}
	if _, ok := cx.PolicyPages[slug]; ok {
		return audit.FinishFailed(r, []audit.Issue{{
			URL: cx.StoreURL,
			Detail: "Policy exists at /policies/" + slug + " but isn't linked from the homepage. " +
				"Add it to the footer so customers (and Google) can find it in one click.",
		}})
	}
	return audit.FinishFailed(r, []audit.Issue{{
		URL:    cx.StoreURL,
		Detail: "No /policies/" + slug + " link detected on the homepage.",
	}})
}
