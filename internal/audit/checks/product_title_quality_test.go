package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestProductTitleQuality(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixture    string
		wantStatus audit.Status
		wantInDetail string
	}{
		{"clean title", "products/complete.html", audit.StatusPass, ""},
		{"shouty + excessive punctuation", "products/title_shouty.html", audit.StatusFail, "ALL CAPS"},
		{"too long", "products/title_too_long.html", audit.StatusFail, "longer than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL:     "https://acme.myshopify.com",
				ProductPages: []*audit.Page{loadFixturePage(t, tc.fixture, "https://acme.myshopify.com/products/p")},
			}
			res := runProductTitleQuality(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
			if tc.wantInDetail != "" {
				if len(res.Issues) == 0 || !contains(res.Issues[0].Detail, tc.wantInDetail) {
					t.Errorf("expected detail to contain %q, got %+v", tc.wantInDetail, res.Issues)
				}
			}
		})
	}
}
