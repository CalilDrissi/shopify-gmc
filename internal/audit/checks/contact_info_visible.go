package checks

import (
	"context"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/gmcauditor/internal/audit"
)

func init() {
	audit.Register(audit.Check{
		Meta:         metaContactInfoVisible,
		Run:          runContactInfoVisible,
		Instructions: instructionsContactInfoVisible,
	})
}

var metaContactInfoVisible = audit.Meta{
	ID:              "contact_info_visible",
	Title:           "Phone, email, or postal address visible to buyers",
	Category:        "trust",
	DefaultSeverity: audit.SeverityWarning,
	AIFixEligible:   true,
}

var (
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	phoneRE = regexp.MustCompile(`\+?\d[\d\s().\-]{7,}\d`)
	// addressKeywords are weak signals; combined with a postal code regex
	// they're a reasonable heuristic.
	addressRE = regexp.MustCompile(`(?i)\b\d{1,5}\s+\w+(\s+\w+){0,4}\s+(street|st|road|rd|avenue|ave|boulevard|blvd|lane|ln|drive|dr|way|court|ct|plaza)\b`)
)

func runContactInfoVisible(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaContactInfoVisible)
	if cx.Homepage == nil || cx.Homepage.Doc == nil {
		return audit.FinishFailed(r, []audit.Issue{{Detail: "homepage was not fetched"}})
	}
	pages := []*audit.Page{cx.Homepage}
	if contact, ok := cx.PolicyPages["contact"]; ok {
		pages = append(pages, contact)
	}
	if contact, ok := cx.PolicyPages["contact-us"]; ok {
		pages = append(pages, contact)
	}

	hasPhone, hasEmail, hasAddress := false, false, false
	for _, p := range pages {
		if p == nil {
			continue
		}
		text := extractText(p.Doc)
		if !hasEmail && emailRE.MatchString(text) {
			hasEmail = true
		}
		if !hasPhone && containsPhone(text) {
			hasPhone = true
		}
		if !hasAddress && addressRE.MatchString(text) {
			hasAddress = true
		}
	}

	if hasPhone || hasEmail || hasAddress {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, []audit.Issue{{
		URL:    cx.StoreURL,
		Detail: "No phone, email, or postal address detected on the homepage or /pages/contact.",
	}})
}

// containsPhone tightens the phone regex by also requiring at least 7 digits
// after stripping non-digits — so "12345" or stray numbers don't false-positive.
func containsPhone(text string) bool {
	for _, m := range phoneRE.FindAllString(text, -1) {
		digits := 0
		for _, r := range m {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits >= 7 {
			return true
		}
	}
	return false
}

func extractText(doc *goquery.Document) string {
	if doc == nil {
		return ""
	}
	var b strings.Builder
	doc.Find("body").Each(func(_ int, s *goquery.Selection) {
		b.WriteString(s.Text())
	})
	// also include mailto: / tel: hrefs
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		if strings.HasPrefix(strings.ToLower(href), "mailto:") || strings.HasPrefix(strings.ToLower(href), "tel:") {
			b.WriteString(" ")
			b.WriteString(href)
		}
	})
	return b.String()
}
