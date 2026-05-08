package checks

import (
	"context"
	"testing"

	"github.com/example/gmcauditor/internal/audit"
)

func TestSitemapAccessible(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		urls       []string
		wantStatus audit.Status
	}{
		{"populated sitemap", manyURLs(20), audit.StatusPass},
		{"sparse sitemap (info)", manyURLs(2), audit.StatusInfo},
		{"empty sitemap", nil, audit.StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx := audit.CheckContext{StoreURL: "https://acme.myshopify.com", SitemapURLs: tc.urls}
			res := runSitemapAccessible(context.Background(), cx)
			mustHaveStatus(t, res.Status, tc.wantStatus)
		})
	}
}

func manyURLs(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = "https://acme.myshopify.com/products/p" + itoaN(i)
	}
	return out
}

func itoaN(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
