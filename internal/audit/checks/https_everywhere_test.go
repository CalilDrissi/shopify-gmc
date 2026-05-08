package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestHTTPSEverywhere(t *testing.T) {
	t.Parallel()

	t.Run("all https", func(t *testing.T) {
		cx := audit.CheckContext{
			StoreURL: "https://acme.myshopify.com",
			Homepage: loadFixturePage(t, "homepage_with_refund.html", "https://acme.myshopify.com/"),
			ProductPages: []*audit.Page{
				loadFixturePage(t, "products/complete.html", "https://acme.myshopify.com/products/p"),
			},
		}
		res := runHTTPSEverywhere(context.Background(), cx)
		mustHaveStatus(t, res.Status, audit.StatusPass)
	})

	t.Run("mixed content on product page", func(t *testing.T) {
		cx := audit.CheckContext{
			StoreURL: "https://acme.myshopify.com",
			Homepage: loadFixturePage(t, "homepage_with_refund.html", "https://acme.myshopify.com/"),
			ProductPages: []*audit.Page{
				loadFixturePage(t, "products/mixed_content.html", "https://acme.myshopify.com/products/insecure"),
			},
		}
		res := runHTTPSEverywhere(context.Background(), cx)
		mustHaveStatus(t, res.Status, audit.StatusFail)
		if len(res.Issues) != 2 { // img + script
			t.Errorf("issues=%d want 2", len(res.Issues))
		}
	})

	t.Run("store URL on http", func(t *testing.T) {
		cx := audit.CheckContext{StoreURL: "http://acme.example.com"}
		res := runHTTPSEverywhere(context.Background(), cx)
		mustHaveStatus(t, res.Status, audit.StatusFail)
	})
}
