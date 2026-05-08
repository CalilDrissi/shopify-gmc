package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestAboutPageExists(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		about      string // path under testdata/shopify, "" = no about page
		wantStatus audit.Status
	}{
		{"substantive about page", "pages/about_substantive.html", audit.StatusPass},
		{"about page too short", "pages/about_too_short.html", audit.StatusFail},
		{"no about page at all", "", audit.StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{
				StoreURL:    "https://acme.myshopify.com",
				PolicyPages: map[string]*audit.Page{},
			}
			if tc.about != "" {
				cx.PolicyPages["about"] = loadFixturePage(t, tc.about, "https://acme.myshopify.com/pages/about")
			}
			res := runAboutPageExists(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
		})
	}
}
