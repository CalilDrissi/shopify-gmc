package checks

import (
	"context"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaTermsOfServiceLinked,
		Run:          runTermsOfServiceLinked,
		Instructions: instructionsTermsOfServiceLinked,
	})
}

var metaTermsOfServiceLinked = audit.Meta{
	ID:              "terms_of_service_linked",
	Title:           "Terms of service linked from the homepage",
	Category:        "policy",
	DefaultSeverity: audit.SeverityError,
	AIFixEligible:   false, // legal copy — no AI rewrite
}

var termsLinkSignals = []string{
	"terms", "terms of service", "terms of use", "terms and conditions",
	"/policies/terms-of-service", "/policies/terms",
}

func runTermsOfServiceLinked(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	return runPolicyLinkCheck(metaTermsOfServiceLinked, "terms-of-service", termsLinkSignals, cx)
}
