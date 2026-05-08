package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestRefundPolicyLinked(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		homepage   string
		wantStatus audit.Status
	}{
		{"footer links to refund policy", "homepage_with_refund.html", audit.StatusPass},
		{"no policy links anywhere", "homepage_no_policies.html", audit.StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL: "https://acme.myshopify.com",
				Homepage: loadFixturePage(t, tc.homepage, "https://acme.myshopify.com/"),
			}
			res := runRefundPolicyLinked(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
		})
	}

	t.Run("page exists but unlinked", func(t *testing.T) {
		cx := audit.CheckContext{
			StoreURL: "https://acme.myshopify.com",
			Homepage: loadFixturePage(t, "homepage_no_policies.html", "https://acme.myshopify.com/"),
			PolicyPages: map[string]*audit.Page{
				"refund-policy": loadFixturePage(t, "homepage_no_policies.html", "https://acme.myshopify.com/policies/refund-policy"),
			},
		}
		res := runRefundPolicyLinked(context.Background(), cx)
		mustHaveStatus(t, res.Status, audit.StatusFail)
		if len(res.Issues) != 1 || !contains(res.Issues[0].Detail, "exists at /policies") {
			t.Errorf("unexpected issues: %+v", res.Issues)
		}
	})

	if instructionsRefundPolicyLinked().WhyItMatters == "" {
		t.Error("missing WhyItMatters copy")
	}
}

// contains is a tiny helper to avoid importing strings just for this.
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
