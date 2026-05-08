// Package-internal: the 9 GMC-native checks. They consume CheckContext.GMC
// (populated by the pipeline's runGMCSync stage) and never touch HTML or
// the AI client — every issue here is sourced from Google's API and
// carries the machine-readable external_issue_code Google returned.
//
// Every check no-ops (returns Pass) when cx.GMC is nil. That's intentional:
// stores without a Google connection skip these checks silently rather
// than producing fake "GMC not connected" warnings on every audit.
package checks

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/example/gmcauditor/internal/audit"
	"github.com/example/gmcauditor/internal/gmc"
)

func init() {
	audit.Register(audit.Check{Meta: metaGMCAccountSuspended, Run: runGMCAccountSuspended, Instructions: instGMCBasic})
	audit.Register(audit.Check{Meta: metaGMCAccountWarning, Run: runGMCAccountWarning, Instructions: instGMCBasic})
	audit.Register(audit.Check{Meta: metaGMCWebsiteNotClaimed, Run: runGMCWebsiteNotClaimed, Instructions: instGMCWebsiteNotClaimed})
	audit.Register(audit.Check{Meta: metaGMCDisapprovalRate, Run: runGMCDisapprovalRate, Instructions: instGMCDisapprovalRate})
	audit.Register(audit.Check{Meta: metaGMCFeedFetchError, Run: runGMCFeedFetchError, Instructions: instGMCFeedFetchError})
	audit.Register(audit.Check{Meta: metaGMCSpecificItemIssue, Run: runGMCSpecificItemIssue, Instructions: instGMCBasic})
	audit.Register(audit.Check{Meta: metaGMCLandingPagePriceMismatch, Run: runGMCLandingPagePriceMismatch, Instructions: instGMCLandingPagePriceMismatch})
	audit.Register(audit.Check{Meta: metaGMCLandingPageAvailabilityMismatch, Run: runGMCLandingPageAvailabilityMismatch, Instructions: instGMCLandingPageAvailabilityMismatch})
	audit.Register(audit.Check{Meta: metaGMCImagePolicyViolation, Run: runGMCImagePolicyViolation, Instructions: instGMCImagePolicyViolation})
}

// ----------------------------------------------------------------------------
// Account-level
// ----------------------------------------------------------------------------

var metaGMCAccountSuspended = audit.Meta{
	ID:              "gmc_account_suspended",
	Title:           "Merchant Center account is not suspended",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityCritical,
	Source:          "gmc_api",
}

func runGMCAccountSuspended(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCAccountSuspended)
	if cx.GMC == nil || cx.GMC.Account == nil {
		return audit.FinishPassed(r)
	}
	var issues []audit.Issue
	for _, ai := range cx.GMC.Account.AccountLevelIssues {
		if ai.Severity != "critical" {
			continue
		}
		issues = append(issues, audit.Issue{
			Detail:       ai.Title,
			Evidence:     ai.Detail,
			ExternalCode: ai.ID,
		})
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

var metaGMCAccountWarning = audit.Meta{
	ID:              "gmc_account_warning",
	Title:           "Merchant Center account has no warnings",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityWarning,
	Source:          "gmc_api",
}

func runGMCAccountWarning(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCAccountWarning)
	if cx.GMC == nil || cx.GMC.Account == nil {
		return audit.FinishPassed(r)
	}
	var issues []audit.Issue
	for _, ai := range cx.GMC.Account.AccountLevelIssues {
		if ai.Severity == "critical" {
			// owned by gmc_account_suspended — don't double-report
			continue
		}
		issues = append(issues, audit.Issue{
			Detail:       ai.Title,
			Evidence:     ai.Detail,
			ExternalCode: ai.ID,
		})
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

var metaGMCWebsiteNotClaimed = audit.Meta{
	ID:              "gmc_website_not_claimed",
	Title:           "Store URL is claimed in Merchant Center",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityWarning,
	Source:          "gmc_api",
}

func runGMCWebsiteNotClaimed(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCWebsiteNotClaimed)
	if cx.GMC == nil || cx.GMC.Account == nil {
		return audit.FinishPassed(r)
	}
	if cx.GMC.Account.WebsiteClaimed {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, []audit.Issue{{
		Detail:       "Your store URL has not been verified and claimed in Merchant Center.",
		Evidence:     "websiteClaimed=false on accountstatuses.get",
		ExternalCode: "website_not_claimed",
	}})
}

// ----------------------------------------------------------------------------
// Product-status aggregates
// ----------------------------------------------------------------------------

var metaGMCDisapprovalRate = audit.Meta{
	ID:              "gmc_product_disapproval_rate",
	Title:           "Product disapproval rate is below threshold",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityWarning, // promoted dynamically
	Source:          "gmc_api",
}

func runGMCDisapprovalRate(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCDisapprovalRate)
	if cx.GMC == nil || len(cx.GMC.Products) == 0 {
		return audit.FinishPassed(r)
	}
	total := len(cx.GMC.Products)
	disapproved := 0
	for _, p := range cx.GMC.Products {
		if hasDisapprovedDestination(p) {
			disapproved++
		}
	}
	if disapproved == 0 {
		return audit.FinishPassed(r)
	}
	rate := float64(disapproved) / float64(total)
	severity := audit.SeverityWarning
	if rate >= 0.05 {
		severity = audit.SeverityCritical
	} else if rate < 0.01 {
		// below the warning floor — pass.
		return audit.FinishPassed(r)
	}
	r.Severity = severity
	r.Status = audit.StatusFail
	r.Issues = []audit.Issue{{
		Detail: fmt.Sprintf("%d of %d products are disapproved (%.1f%%).", disapproved, total, rate*100),
		Evidence: "Per-product item-level issues are listed under specific item issues.",
		ExternalCode: "high_disapproval_rate",
	}}
	return r
}

func hasDisapprovedDestination(p gmc.ProductStatus) bool {
	for _, d := range p.DestinationStatuses {
		if d.Status == "disapproved" {
			return true
		}
		if len(d.DisapprovedCountries) > 0 {
			return true
		}
	}
	return false
}

var metaGMCFeedFetchError = audit.Meta{
	ID:              "gmc_feed_fetch_error",
	Title:           "Product feed fetched without errors",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityCritical,
	Source:          "gmc_api",
}

func runGMCFeedFetchError(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCFeedFetchError)
	if cx.GMC == nil || len(cx.GMC.Feeds) == 0 {
		return audit.FinishPassed(r)
	}
	var issues []audit.Issue
	for _, f := range cx.GMC.Feeds {
		if f.ProcessingStatus == "success" && len(f.Errors) == 0 {
			continue
		}
		// Aggregate the top error code if any.
		code := "feed_processing_failure"
		msg := "Feed processing failed."
		if len(f.Errors) > 0 {
			code = f.Errors[0].Code
			msg = f.Errors[0].Message
		} else if f.ProcessingStatus != "success" {
			msg = "Feed processing status: " + f.ProcessingStatus
		}
		issues = append(issues, audit.Issue{
			Detail:       fmt.Sprintf("Feed %s (%s/%s): %s", f.DatafeedID, f.Country, f.Language, msg),
			Evidence:     fmt.Sprintf("itemsTotal=%d itemsValid=%d", f.ItemsTotal, f.ItemsValid),
			ExternalCode: code,
		})
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

// ----------------------------------------------------------------------------
// Item-level issue aggregations (the long tail)
// ----------------------------------------------------------------------------

var metaGMCSpecificItemIssue = audit.Meta{
	ID:              "gmc_specific_item_issue",
	Title:           "Item-level issues reported by Google",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityWarning,
	Source:          "gmc_api",
}

// runGMCSpecificItemIssue groups every item-level issue by its `code`,
// then surfaces one Issue row per distinct code carrying the count of
// affected products. We deliberately exclude codes that have their own
// dedicated checks (price/availability mismatch, image policy) so the
// dashboard isn't double-counting.
func runGMCSpecificItemIssue(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCSpecificItemIssue)
	if cx.GMC == nil || len(cx.GMC.Products) == 0 {
		return audit.FinishPassed(r)
	}
	dedicated := map[string]bool{
		"landing_page_price_mismatch":        true,
		"landing_page_availability_mismatch": true,
	}
	type bucket struct {
		count int
		example gmc.ItemLevelIssue
		products map[string]bool
	}
	groups := map[string]*bucket{}
	for _, p := range cx.GMC.Products {
		for _, ii := range p.ItemLevelIssues {
			if dedicated[ii.Code] || isImagePolicyCode(ii.Code) {
				continue
			}
			b, ok := groups[ii.Code]
			if !ok {
				b = &bucket{example: ii, products: map[string]bool{}}
				groups[ii.Code] = b
			}
			b.products[p.ProductID] = true
			b.count = len(b.products)
		}
	}
	codes := make([]string, 0, len(groups))
	for c := range groups {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	var issues []audit.Issue
	for _, code := range codes {
		b := groups[code]
		issues = append(issues, audit.Issue{
			Detail: fmt.Sprintf("%s — affects %d product%s.",
				b.example.Description, b.count, plural(b.count)),
			Evidence:     b.example.Detail,
			ExternalCode: code,
		})
	}
	if len(issues) == 0 {
		return audit.FinishPassed(r)
	}
	return audit.FinishFailed(r, issues)
}

var metaGMCLandingPagePriceMismatch = audit.Meta{
	ID:              "gmc_landing_page_price_mismatch",
	Title:           "Landing-page prices match feed prices",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityCritical,
	Source:          "gmc_api",
}

func runGMCLandingPagePriceMismatch(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCLandingPagePriceMismatch)
	return runItemCodeCheck(r, cx, "landing_page_price_mismatch", "Price on landing page does not match feed.")
}

var metaGMCLandingPageAvailabilityMismatch = audit.Meta{
	ID:              "gmc_landing_page_availability_mismatch",
	Title:           "Landing-page availability matches feed availability",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityWarning,
	Source:          "gmc_api",
}

func runGMCLandingPageAvailabilityMismatch(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCLandingPageAvailabilityMismatch)
	return runItemCodeCheck(r, cx, "landing_page_availability_mismatch", "Availability on landing page does not match feed.")
}

var metaGMCImagePolicyViolation = audit.Meta{
	ID:              "gmc_image_policy_violation",
	Title:           "Product images comply with Google's image policy",
	Category:        "gmc",
	DefaultSeverity: audit.SeverityWarning,
	Source:          "gmc_api",
}

func runGMCImagePolicyViolation(_ context.Context, cx audit.CheckContext) audit.CheckResult {
	r := audit.NewResult(metaGMCImagePolicyViolation)
	if cx.GMC == nil || len(cx.GMC.Products) == 0 {
		return audit.FinishPassed(r)
	}
	type bucket struct {
		products map[string]bool
		example  gmc.ItemLevelIssue
	}
	groups := map[string]*bucket{}
	for _, p := range cx.GMC.Products {
		for _, ii := range p.ItemLevelIssues {
			if !isImagePolicyCode(ii.Code) {
				continue
			}
			b, ok := groups[ii.Code]
			if !ok {
				b = &bucket{products: map[string]bool{}, example: ii}
				groups[ii.Code] = b
			}
			b.products[p.ProductID] = true
		}
	}
	if len(groups) == 0 {
		return audit.FinishPassed(r)
	}
	codes := make([]string, 0, len(groups))
	for c := range groups {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	var issues []audit.Issue
	for _, code := range codes {
		b := groups[code]
		issues = append(issues, audit.Issue{
			Detail: fmt.Sprintf("%s — affects %d product%s.",
				b.example.Description, len(b.products), plural(len(b.products))),
			Evidence:     b.example.Detail,
			ExternalCode: code,
		})
	}
	return audit.FinishFailed(r, issues)
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func runItemCodeCheck(r audit.CheckResult, cx audit.CheckContext, code, summary string) audit.CheckResult {
	if cx.GMC == nil || len(cx.GMC.Products) == 0 {
		return audit.FinishPassed(r)
	}
	affected := map[string]gmc.ProductStatus{}
	var example gmc.ItemLevelIssue
	for _, p := range cx.GMC.Products {
		for _, ii := range p.ItemLevelIssues {
			if ii.Code == code {
				affected[p.ProductID] = p
				example = ii
				break
			}
		}
	}
	if len(affected) == 0 {
		return audit.FinishPassed(r)
	}
	var issues []audit.Issue
	// One issue per affected product so the report can deep-link.
	productIDs := make([]string, 0, len(affected))
	for id := range affected {
		productIDs = append(productIDs, id)
	}
	sort.Strings(productIDs)
	for _, id := range productIDs {
		p := affected[id]
		issues = append(issues, audit.Issue{
			URL:          p.Link,
			ProductTitle: p.Title,
			Detail:       summary,
			Evidence:     example.Detail,
			ExternalCode: code,
		})
	}
	return audit.FinishFailed(r, issues)
}

func isImagePolicyCode(code string) bool {
	c := strings.ToLower(code)
	return strings.HasPrefix(c, "image_link_") || strings.Contains(c, "image_policy") ||
		c == "promotional_overlay" || c == "missing_image_link"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ----------------------------------------------------------------------------
// Hand-written fix instructions per check
// ----------------------------------------------------------------------------

func instGMCBasic() audit.FixInstructions {
	return audit.FixInstructions{
		Summary:      "Open Merchant Center → Diagnostics and follow Google's published resolution steps.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "varies",
		Steps: []audit.Step{
			{Number: 1, Action: "Sign in to merchants.google.com", Path: "Merchant Center"},
			{Number: 2, Action: "Open the Diagnostics page", Path: "Performance → Diagnostics"},
			{Number: 3, Action: "Click each issue to read Google's specific resolution guidance"},
			{Number: 4, Action: "Re-run this audit after fixing to confirm the issue cleared"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/160491",
		WhyItMatters: "Account-level issues block products from showing in Shopping ads and free listings until cleared.",
	}
}

func instGMCWebsiteNotClaimed() audit.FixInstructions {
	return audit.FixInstructions{
		Summary:      "Verify and claim your store URL in Merchant Center.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "10 min",
		Steps: []audit.Step{
			{Number: 1, Action: "Open Merchant Center → Tools → Business information → Website", Path: "Tools"},
			{Number: 2, Action: "Click Verify website and follow the chosen verification method (HTML tag, file upload, or Google Analytics)"},
			{Number: 3, Action: "Click Claim once verification succeeds"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/176793",
		WhyItMatters: "An unclaimed website can't serve products through Merchant Center; claiming also unlocks free listings.",
	}
}

func instGMCDisapprovalRate() audit.FixInstructions {
	return audit.FixInstructions{
		Summary:      "Bring the disapproval rate below 1% by fixing item-level issues in priority order.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "varies",
		Steps: []audit.Step{
			{Number: 1, Action: "Sort the Item-level issues report by affected products descending"},
			{Number: 2, Action: "Address the largest cohort first (one fix usually clears many products)"},
			{Number: 3, Action: "Resubmit the feed and wait for the next processing pass"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/9370694",
		WhyItMatters: "Sustained high disapproval rates trigger Merchant Center account suspensions.",
	}
}

func instGMCFeedFetchError() audit.FixInstructions {
	return audit.FixInstructions{
		Summary:      "Fix the feed errors so Google can reprocess the file.",
		Difficulty:   audit.DifficultyTechnical,
		TimeEstimate: "30 min",
		Steps: []audit.Step{
			{Number: 1, Action: "Open Merchant Center → Products → Feeds → Diagnostics"},
			{Number: 2, Action: "Read the Errors tab — each entry includes the failing line number"},
			{Number: 3, Action: "Patch the upstream feed source and force a re-fetch"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/188494",
		WhyItMatters: "A failing feed prevents new product data from reaching Google, so the catalogue ages out.",
	}
}

func instGMCLandingPagePriceMismatch() audit.FixInstructions {
	return audit.FixInstructions{
		Summary:      "Make the landing-page price match the price in the feed (within Google's tolerance).",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "5–15 min per product",
		Steps: []audit.Step{
			{Number: 1, Action: "Open the affected product in Shopify admin"},
			{Number: 2, Action: "Confirm the storefront price matches the price you submit in the feed"},
			{Number: 3, Action: "If you display a discounted price, also include a `sale_price` in the feed"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/2752085",
		WhyItMatters: "Mismatched prices are treated as bait-and-switch and disapprove the product.",
	}
}

func instGMCLandingPageAvailabilityMismatch() audit.FixInstructions {
	return audit.FixInstructions{
		Summary:      "Sync availability between your feed and your live product page.",
		Difficulty:   audit.DifficultyEasy,
		TimeEstimate: "5 min per product",
		Steps: []audit.Step{
			{Number: 1, Action: "Compare the `availability` attribute in your feed to the storefront state"},
			{Number: 2, Action: "If items frequently go in/out of stock, enable Google's automatic-item-updates feature"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/3246284",
		WhyItMatters: "Customers clicking ads land on out-of-stock items, hurting conversion + ad spend ROI.",
	}
}

func instGMCImagePolicyViolation() audit.FixInstructions {
	return audit.FixInstructions{
		Summary:      "Replace product images that contain promotional overlays, watermarks, or excess whitespace.",
		Difficulty:   audit.DifficultyModerate,
		TimeEstimate: "10 min per product",
		Steps: []audit.Step{
			{Number: 1, Action: "Strip overlay text/logos/badges (\"Sale\", \"Best price\", etc.) from product images"},
			{Number: 2, Action: "Use a clean background, ideally white or transparent"},
			{Number: 3, Action: "Re-upload the corrected image and resubmit the feed"},
		},
		DocsURL:      "https://support.google.com/merchants/answer/6324350",
		WhyItMatters: "Promotional imagery is a hard policy violation and disapproves the product entirely.",
	}
}
