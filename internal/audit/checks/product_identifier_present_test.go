package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestProductIdentifierPresent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixture    string
		wantStatus audit.Status
	}{
		{"with gtin13", "products/with_gtin.html", audit.StatusPass},
		{"identifier_exists=false (handmade)", "products/identifier_exists_false.html", audit.StatusPass},
		{"missing identifier", "products/missing_identifier.html", audit.StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL:     "https://acme.myshopify.com",
				ProductPages: []*audit.Page{loadFixturePage(t, tc.fixture, "https://acme.myshopify.com/products/p")},
			}
			res := runProductIdentifierPresent(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
		})
	}
}
