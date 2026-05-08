package checks

import (
	"context"
	"strings"

	"github.com/temoto/robotstxt"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaRobotsTxtValid,
		Run:          runRobotsTxtValid,
		Instructions: instructionsRobotsTxtValid,
	})
}

var metaRobotsTxtValid = audit.Meta{
	ID:              "robots_txt_valid",
	Title:           "robots.txt parses and doesn't block product pages",
	Category:        "infra",
	DefaultSeverity: audit.SeverityCritical,
	AIFixEligible:   false, // infra config; not for AI rewrite
}

func runRobotsTxtValid(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaRobotsTxtValid)
	if strings.TrimSpace(cx.RobotsTxt) == "" {
		return audit.FinishFailed(r, []audit.Issue{{
			URL:    cx.StoreURL + "/robots.txt",
			Detail: "robots.txt is missing or empty.",
		}})
	}
	parsed, err := robotstxt.FromString(cx.RobotsTxt)
	if err != nil {
		return audit.FinishFailed(r, []audit.Issue{{
			URL:    cx.StoreURL + "/robots.txt",
			Detail: "robots.txt failed to parse: " + err.Error(),
		}})
	}
	// Test with the Googlebot user-agent — that's what GMC indirectly cares about.
	bot := parsed.FindGroup("Googlebot")
	if bot == nil {
		bot = parsed.FindGroup("*")
	}
	if bot == nil {
		return audit.FinishPassed(r)
	}
	// Sample paths that MUST be crawlable for GMC to function.
	probes := []string{"/", "/products/sample", "/collections/all", "/sitemap.xml"}
	var issues []audit.Issue
	for _, path := range probes {
		if !bot.Test(path) {
			issues = append(issues, audit.Issue{
				URL:    cx.StoreURL + "/robots.txt",
				Detail: "robots.txt disallows " + path + " for Googlebot.",
			})
		}
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}
