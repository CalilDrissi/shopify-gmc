package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestContactInfoVisible(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		homepage   string
		wantStatus audit.Status
	}{
		{"phone+email+address present", "homepage_with_contact.html", audit.StatusPass},
		{"nothing present", "homepage_no_policies.html", audit.StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL: "https://acme.myshopify.com",
				Homepage: loadFixturePage(t, tc.homepage, "https://acme.myshopify.com/"),
			}
			res := runContactInfoVisible(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
		})
	}
}
