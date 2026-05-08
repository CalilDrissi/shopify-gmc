package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestCanonicalTagsPresent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixture    string
		wantStatus audit.Status
		wantIssues int
	}{
		{"canonical pointing at self", "products/complete.html", audit.StatusPass, 0},
		{"missing canonical", "products/no_canonical.html", audit.StatusFail, 1},
		{"canonical pointing at homepage", "products/canonical_to_homepage.html", audit.StatusFail, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL:     "https://acme.myshopify.com",
				ProductPages: []*audit.Page{loadFixturePage(t, tc.fixture, "https://acme.myshopify.com/products/p")},
			}
			res := runCanonicalTagsPresent(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
			if len(res.Issues) != tc.wantIssues {
				t.Errorf("issues=%d want %d (issues=%+v)", len(res.Issues), tc.wantIssues, res.Issues)
			}
		})
	}
}
