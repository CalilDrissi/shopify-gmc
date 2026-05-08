package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestProductImagesPresent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixture    string
		wantStatus audit.Status
	}{
		{"image in schema + cdn img tag", "products/complete.html", audit.StatusPass},
		{"no image at all", "products/no_image.html", audit.StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL:     "https://acme.myshopify.com",
				ProductPages: []*audit.Page{loadFixturePage(t, tc.fixture, "https://acme.myshopify.com/products/p")},
			}
			res := runProductImagesPresent(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
		})
	}
}
