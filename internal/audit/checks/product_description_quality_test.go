package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestProductDescriptionQuality(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fixtures   []string
		wantStatus audit.Status
		wantInDetail string
	}{
		{"good description", []string{"products/long_description.html"}, audit.StatusPass, ""},
		{"too short", []string{"products/short_description.html"}, audit.StatusFail, "shorter than"},
		{"raw HTML artifacts", []string{"products/html_artifact_description.html"}, audit.StatusFail, "raw HTML artifact"},
		{"identical descriptions across products",
			[]string{"products/short_description.html", "products/short_description.html"},
			audit.StatusFail, "identical across"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{StoreURL: "https://acme.myshopify.com"}
			for i, f := range tc.fixtures {
				cx.ProductPages = append(cx.ProductPages,
					loadFixturePage(t, f, "https://acme.myshopify.com/products/p"+itoaInt(i)))
			}
			res := runProductDescriptionQuality(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
			if tc.wantInDetail != "" {
				found := false
				for _, iss := range res.Issues {
					if contains(iss.Detail, tc.wantInDetail) {
						found = true
					}
				}
				if !found {
					t.Errorf("expected issue containing %q, got %+v", tc.wantInDetail, res.Issues)
				}
			}
		})
	}
}
