// Package audit defines the framework for deterministic GMC compliance checks.
//
// Each check lives in /internal/audit/checks/{name}.go and registers itself
// with the audit package via init(). The runner imports
// `_ "internal/audit/checks"` to trigger registration, then calls All() to
// enumerate every check.
//
// Detection is pure Go — checks never import the AI client. AI fix
// suggestions are layered on top later, controlled by Meta.AIFixEligible.
package audit

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/example/gmcauditor/internal/gmc"
)

// ----------------------------------------------------------------------------
// Inputs the runner builds from a crawl and hands to every check.
// ----------------------------------------------------------------------------

type Page struct {
	URL        string
	StatusCode int
	HTML       string
	Doc        *goquery.Document
	Headers    http.Header
	FetchedAt  time.Time
	FetchError error
}

type CheckContext struct {
	StoreURL        string
	Homepage        *Page
	PolicyPages     map[string]*Page // keyed by canonical slug, e.g. "refund-policy"
	ProductPages    []*Page
	CollectionPages []*Page
	SitemapURLs     []string
	RobotsTxt       string

	// GMC is populated by the GMC sync stage when an active connection
	// exists for this store. nil → no connection or sync failed; checks
	// must guard with `if cx.GMC == nil`.
	GMC *GMCContext
}

// GMCContext is the slice of Google Merchant Center data the GMC-native
// checks consume. Populated by the pipeline's runGMCSync stage; never
// touched by the crawler.
type GMCContext struct {
	MerchantID string
	Account    *gmc.AccountStatus
	Products   []gmc.ProductStatus
	Feeds      []gmc.DatafeedStatus
}

// ----------------------------------------------------------------------------
// Per-check static metadata.
// ----------------------------------------------------------------------------

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityError    Severity = "error"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

type Meta struct {
	ID              string   // unique e.g. "product_schema_complete"
	Title           string   // human-friendly
	Category        string   // "structured-data", "policy", "content", "infra"
	DefaultSeverity Severity // baseline severity when failing
	AIFixEligible   bool     // legal/policy checks must be false
	// Source labels every issue this check produces. Default "crawler"
	// (via the persister) — GMC-native checks set "gmc_api" so the report
	// can split crawler vs. Google-reported findings into tabs.
	Source string
}

// ----------------------------------------------------------------------------
// Per-check structured fix instructions (always hand-written, never AI).
// ----------------------------------------------------------------------------

type Difficulty string

const (
	DifficultyEasy      Difficulty = "easy"
	DifficultyModerate  Difficulty = "moderate"
	DifficultyTechnical Difficulty = "technical"
)

type Step struct {
	Number int    `json:"number"`
	Action string `json:"action"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type FixInstructions struct {
	Summary      string     `json:"summary"`
	Difficulty   Difficulty `json:"difficulty"`
	TimeEstimate string     `json:"time_estimate"`
	Steps        []Step     `json:"steps"`
	DocsURL      string     `json:"docs_url,omitempty"`
	WhyItMatters string     `json:"why_it_matters"`
}

// ----------------------------------------------------------------------------
// Check results: per-product or per-page issues attached to a check run.
// ----------------------------------------------------------------------------

type Status int

const (
	StatusPass Status = iota
	StatusFail
	StatusInfo
)

func (s Status) String() string {
	switch s {
	case StatusPass:
		return "pass"
	case StatusFail:
		return "fail"
	case StatusInfo:
		return "info"
	}
	return "unknown"
}

type Issue struct {
	URL          string `json:"url,omitempty"`
	ProductTitle string `json:"product_title,omitempty"`
	Detail       string `json:"detail"`
	Evidence     string `json:"evidence,omitempty"`
	// ExternalCode is Google's machine-readable code for GMC-sourced issues
	// (e.g. "missing_gtin", "image_link_broken"). Empty for crawler issues.
	ExternalCode string `json:"external_code,omitempty"`
}

type CheckResult struct {
	Meta      Meta
	Status    Status
	Severity  Severity
	Issues    []Issue
	StartedAt time.Time
	Duration  time.Duration
}

// ----------------------------------------------------------------------------
// Check definition + global registry. Checks register themselves in init().
// ----------------------------------------------------------------------------

type Check struct {
	Meta         Meta
	Run          func(ctx context.Context, cx CheckContext) CheckResult
	Instructions func() FixInstructions
}

var (
	regMu    sync.RWMutex
	registry = map[string]Check{}
)

func Register(c Check) {
	if c.Meta.ID == "" {
		panic("audit: cannot register check with empty ID")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, exists := registry[c.Meta.ID]; exists {
		panic("audit: duplicate check ID: " + c.Meta.ID)
	}
	registry[c.Meta.ID] = c
}

func All() []Check {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Check, 0, len(registry))
	for _, c := range registry {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.ID < out[j].Meta.ID })
	return out
}

func Get(id string) (Check, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	c, ok := registry[id]
	return c, ok
}

// ----------------------------------------------------------------------------
// Helpers for check authors.
// ----------------------------------------------------------------------------

// NewResult starts a CheckResult tracking start time and the default severity.
func NewResult(meta Meta) CheckResult {
	return CheckResult{
		Meta:      meta,
		Status:    StatusPass,
		Severity:  meta.DefaultSeverity,
		StartedAt: time.Now(),
	}
}

// FinishFailed marks the result failed and stamps duration.
func FinishFailed(r CheckResult, issues []Issue) CheckResult {
	r.Status = StatusFail
	r.Issues = issues
	r.Duration = time.Since(r.StartedAt)
	return r
}

// FinishPassed marks the result passed and stamps duration.
func FinishPassed(r CheckResult) CheckResult {
	r.Status = StatusPass
	r.Duration = time.Since(r.StartedAt)
	return r
}

// FinishInfo is for diagnostic checks that surface info without failing.
func FinishInfo(r CheckResult, issues []Issue) CheckResult {
	r.Status = StatusInfo
	r.Issues = issues
	r.Duration = time.Since(r.StartedAt)
	return r
}
