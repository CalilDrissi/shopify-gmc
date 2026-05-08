package checks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestRobotsTxtValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixture    string
		wantStatus audit.Status
	}{
		{"good shopify default", "robots/good.txt", audit.StatusPass},
		{"blocks /products", "robots/blocks_products.txt", audit.StatusFail},
		{"empty robots.txt", "robots/empty.txt", audit.StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(repoRoot(t), "testdata", "shopify", tc.fixture))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			cx := audit.CheckContext{StoreURL: "https://acme.myshopify.com", RobotsTxt: string(b)}
			res := runRobotsTxtValid(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
		})
	}
}
