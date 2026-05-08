package checks

import (
	"context"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaSitemapAccessible,
		Run:          runSitemapAccessible,
		Instructions: instructionsSitemapAccessible,
	})
}

var metaSitemapAccessible = audit.Meta{
	ID:              "sitemap_accessible",
	Title:           "sitemap.xml is reachable and lists products",
	Category:        "infra",
	DefaultSeverity: audit.SeverityWarning,
	AIFixEligible:   false,
}

func runSitemapAccessible(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaSitemapAccessible)
	if len(cx.SitemapURLs) == 0 {
		return audit.FinishFailed(r, []audit.Issue{{
			URL:    cx.StoreURL + "/sitemap.xml",
			Detail: "Sitemap was not reachable, returned no URLs, or was not declared in robots.txt.",
		}})
	}
	// Surface as info if sitemap is small (<5 entries) — usable but suspicious.
	if len(cx.SitemapURLs) < 5 {
		return audit.FinishInfo(r, []audit.Issue{{
			URL:    cx.StoreURL + "/sitemap.xml",
			Detail: "Sitemap is reachable but contains fewer than 5 URLs — likely a partial crawl or a brand-new store.",
		}})
	}
	return audit.FinishPassed(r)
}
