package checks

import (
	"context"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaPrivacyPolicyLinked,
		Run:          runPrivacyPolicyLinked,
		Instructions: instructionsPrivacyPolicyLinked,
	})
}

var metaPrivacyPolicyLinked = audit.Meta{
	ID:              "privacy_policy_linked",
	Title:           "Privacy policy linked from the homepage",
	Category:        "policy",
	DefaultSeverity: audit.SeverityCritical,
	AIFixEligible:   false, // legal copy — no AI rewrite
}

var privacyLinkSignals = []string{
	"privacy", "privacy policy", "data protection",
	"/policies/privacy-policy",
}

func runPrivacyPolicyLinked(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	return runPolicyLinkCheck(metaPrivacyPolicyLinked, "privacy-policy", privacyLinkSignals, cx)
}
