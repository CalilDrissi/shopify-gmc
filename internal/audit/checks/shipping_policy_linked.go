package checks

import (
	"context"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaShippingPolicyLinked,
		Run:          runShippingPolicyLinked,
		Instructions: instructionsShippingPolicyLinked,
	})
}

var metaShippingPolicyLinked = audit.Meta{
	ID:              "shipping_policy_linked",
	Title:           "Shipping policy linked from the homepage",
	Category:        "policy",
	DefaultSeverity: audit.SeverityError,
	AIFixEligible:   true,
}

var shippingLinkSignals = []string{
	"shipping", "delivery", "postage",
	"/policies/shipping-policy", "/policies/delivery-policy",
}

func runShippingPolicyLinked(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	return runPolicyLinkCheck(metaShippingPolicyLinked, "shipping-policy", shippingLinkSignals, cx)
}
