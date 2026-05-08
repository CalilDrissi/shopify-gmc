package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

// One table-driven test exercises the three "linked from homepage" policy
// checks in turn 2 (refund is in turn 1's test). Sharing the test keeps the
// per-check files lean while still hitting both pass and fail paths for each.
func TestPolicyLinkChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		runner      func(context.Context, audit.CheckContext) audit.CheckResult
		homepage    string
		wantStatus  audit.Status
	}{
		{"shipping pass", runShippingPolicyLinked, "homepage_with_contact.html", audit.StatusPass},
		{"shipping fail", runShippingPolicyLinked, "homepage_no_policies.html", audit.StatusFail},
		{"privacy pass", runPrivacyPolicyLinked, "homepage_with_contact.html", audit.StatusPass},
		{"privacy fail", runPrivacyPolicyLinked, "homepage_no_policies.html", audit.StatusFail},
		{"terms pass", runTermsOfServiceLinked, "homepage_with_contact.html", audit.StatusPass},
		{"terms fail", runTermsOfServiceLinked, "homepage_no_policies.html", audit.StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL: "https://acme.myshopify.com",
				Homepage: loadFixturePage(t, tc.homepage, "https://acme.myshopify.com/"),
			}
			res := tc.runner(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
		})
	}
}

func TestLegalPoliciesAreNotAIFixEligible(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"privacy_policy_linked", "terms_of_service_linked"} {
		c, ok := audit.Get(id)
		if !ok {
			t.Fatalf("%s not registered", id)
		}
		if c.Meta.AIFixEligible {
			t.Errorf("%s must NOT be AIFixEligible (legal liability)", id)
		}
	}
}
