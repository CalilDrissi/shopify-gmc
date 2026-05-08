package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestProductSchemaPresent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixture    string
		wantStatus audit.Status
		wantIssues int
	}{
		{"with schema", "products/complete.html", audit.StatusPass, 0},
		{"without schema", "products/no_schema.html", audit.StatusFail, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL:     "https://acme.myshopify.com",
				ProductPages: []*audit.Page{loadFixturePage(t, tc.fixture, "https://acme.myshopify.com/products/p")},
			}
			res := runProductSchemaPresent(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
			if len(res.Issues) != tc.wantIssues {
				t.Errorf("issues=%d want %d", len(res.Issues), tc.wantIssues)
			}
		})
	}
	if instructionsProductSchemaPresent().Difficulty == "" {
		t.Error("difficulty empty")
	}
}
