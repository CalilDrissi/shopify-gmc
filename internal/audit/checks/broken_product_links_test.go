package checks

import (
	"context"
	"errors"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestBrokenProductLinks(t *testing.T) {
	t.Parallel()
	good := loadFixturePage(t, "products/complete.html", "https://acme.myshopify.com/products/wb")

	t.Run("all 200s pass", func(t *testing.T) {
		cx := audit.CheckContext{
			StoreURL:     "https://acme.myshopify.com",
			Homepage:     loadFixturePage(t, "homepage_with_contact.html", "https://acme.myshopify.com/"),
			ProductPages: []*audit.Page{good},
		}
		res := runBrokenProductLinks(context.Background(), cx)
		mustHaveStatus(t, res.Status, audit.StatusPass)
	})

	t.Run("404 product fails", func(t *testing.T) {
		broken := *good
		broken.URL = "https://acme.myshopify.com/products/missing"
		broken.StatusCode = 404
		cx := audit.CheckContext{
			StoreURL:     "https://acme.myshopify.com",
			Homepage:     loadFixturePage(t, "homepage_with_contact.html", "https://acme.myshopify.com/"),
			ProductPages: []*audit.Page{good, &broken},
		}
		res := runBrokenProductLinks(context.Background(), cx)
		mustHaveStatus(t, res.Status, audit.StatusFail)
		if len(res.Issues) != 1 || !contains(res.Issues[0].Detail, "404") {
			t.Errorf("issue mismatch: %+v", res.Issues)
		}
	})

	t.Run("policy fetch error fails", func(t *testing.T) {
		bad := &audit.Page{URL: "https://acme.myshopify.com/policies/refund-policy", FetchError: errors.New("dial timeout")}
		cx := audit.CheckContext{
			StoreURL: "https://acme.myshopify.com",
			Homepage: loadFixturePage(t, "homepage_with_contact.html", "https://acme.myshopify.com/"),
			PolicyPages: map[string]*audit.Page{"refund-policy": bad},
		}
		res := runBrokenProductLinks(context.Background(), cx)
		mustHaveStatus(t, res.Status, audit.StatusFail)
	})
}
