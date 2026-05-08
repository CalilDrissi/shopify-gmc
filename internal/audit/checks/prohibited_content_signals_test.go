package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestProhibitedContentSignals(t *testing.T) {
	t.Parallel()

	t.Run("clean store passes", func(t *testing.T) {
		cx := audit.CheckContext{
			StoreURL: "https://acme.myshopify.com",
			Homepage: loadFixturePage(t, "homepage_with_contact.html", "https://acme.myshopify.com/"),
			ProductPages: []*audit.Page{
				loadFixturePage(t, "products/complete.html", "https://acme.myshopify.com/products/wb"),
			},
		}
		res := runProhibitedContentSignals(context.Background(), cx)
		mustHaveStatus(t, res.Status, audit.StatusPass)
	})

	t.Run("CBD signal surfaces as INFO not FAIL", func(t *testing.T) {
		cx := audit.CheckContext{
			StoreURL: "https://acme.myshopify.com",
			Homepage: loadFixturePage(t, "homepage_with_contact.html", "https://acme.myshopify.com/"),
			ProductPages: []*audit.Page{
				loadFixturePage(t, "products/cbd_signal.html", "https://acme.myshopify.com/products/cbd"),
			},
		}
		res := runProhibitedContentSignals(context.Background(), cx)
		// Heads-up: this check NEVER fails — it surfaces info, never blocks.
		mustHaveStatus(t, res.Status, audit.StatusInfo)
		if len(res.Issues) == 0 {
			t.Error("expected at least one CBD-category issue")
		}
		// And it must NOT be AI-fix-eligible (legal/compliance review).
		c, _ := audit.Get(metaProhibitedContentSignals.ID)
		if c.Meta.AIFixEligible {
			t.Error("prohibited_content_signals must NOT be AIFixEligible")
		}
	})
}
