package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestCurrencyConsistency(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixtures   []string
		wantStatus audit.Status
		wantIssues int
	}{
		{"all USD", []string{"products/complete.html", "products/with_gtin.html"}, audit.StatusPass, 0},
		{"USD + EUR mixed", []string{"products/complete.html", "products/with_gtin.html", "products/eur_currency.html"}, audit.StatusFail, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{StoreURL: "https://acme.myshopify.com"}
			for i, f := range tc.fixtures {
				cx.ProductPages = append(cx.ProductPages,
					loadFixturePage(t, f, "https://acme.myshopify.com/products/p"+itoaInt(i)))
			}
			res := runCurrencyConsistency(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
			if len(res.Issues) != tc.wantIssues {
				t.Errorf("issues=%d want %d (%+v)", len(res.Issues), tc.wantIssues, res.Issues)
			}
		})
	}
}
