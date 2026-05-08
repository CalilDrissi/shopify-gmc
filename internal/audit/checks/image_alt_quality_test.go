package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestImageAltQuality(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixture    string
		wantStatus audit.Status
		wantIssues int
	}{
		{"no cdn imgs to check (passes vacuously)", "products/complete.html", audit.StatusPass, 0},
		{"filename + sku + empty alts", "products/bad_alt.html", audit.StatusFail, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL:     "https://acme.myshopify.com",
				ProductPages: []*audit.Page{loadFixturePage(t, tc.fixture, "https://acme.myshopify.com/products/p")},
			}
			res := runImageAltQuality(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
			if len(res.Issues) != tc.wantIssues {
				t.Errorf("issues=%d want %d (%+v)", len(res.Issues), tc.wantIssues, res.Issues)
			}
		})
	}
}
