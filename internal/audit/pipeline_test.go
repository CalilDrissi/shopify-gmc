package audit_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/ai"
	"github.com/example/gmcauditor/internal/audit"
	_ "github.com/example/gmcauditor/internal/audit/checks"
)

// TestPipelineDeterminism runs the full 7-stage pipeline against a fixed
// CheckContext using the deterministic mock AI client. Two runs must produce
// identical outputs (modulo wall-clock fields, which we strip before compare).
func TestPipelineDeterminism(t *testing.T) {
	t.Parallel()
	cx := buildFixtureContext(t)

	clock := func() time.Time { return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC) }

	makePipeline := func() *audit.Pipeline {
		return &audit.Pipeline{
			Crawl: func(ctx context.Context, _ string) (audit.CheckContext, error) { return cx, nil },
			AI:    ai.NewMockClient(),
			Now:   clock,
		}
	}
	in := audit.AuditInput{
		AuditID:      uuid.MustParse("aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"),
		TenantID:     uuid.MustParse("bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"),
		StoreID:      uuid.MustParse("cccccccc-cccc-4ccc-cccc-cccccccccccc"),
		StoreURL:     "https://acme.myshopify.com",
		StoreName:    "Acme Goods",
		StoreContext: "Bay Area textile brand selling organic cotton aprons.",
		Trigger:      "manual",
	}

	out1, err := makePipeline().Run(context.Background(), in)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	out2, err := makePipeline().Run(context.Background(), in)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}

	stripVolatile(out1)
	stripVolatile(out2)

	if !reflect.DeepEqual(out1, out2) {
		t.Errorf("pipeline is not deterministic with mocked AI\nrun1=%+v\nrun2=%+v", out1, out2)
	}

	// Sanity: pipeline must have run all 8 stages and ended at "persist".
	// Stage 2.5 (gmc_sync) is "skipped" when no GMCSyncFn is set on the
	// pipeline, but it still appears in the stage list.
	if len(out1.Stages) != 8 {
		t.Errorf("got %d stages, want 8: %+v", len(out1.Stages), out1.Stages)
	}
	want := []string{"validate", "crawl", "gmc_sync", "detect", "score", "enrich", "summarize", "persist"}
	for i, s := range out1.Stages {
		if s.Name != want[i] {
			t.Errorf("stage %d = %q, want %q", i, s.Name, want[i])
		}
	}
	if out1.Score < 1 || out1.Score > 100 {
		t.Errorf("implausible score: %d", out1.Score)
	}
	if out1.RiskLevel == "" {
		t.Error("risk level not set")
	}
	if out1.Summary == "" {
		t.Error("summary not set")
	}
}

// TestPipelineFallback_SummaryOnAIError shows stages 5/6 are best-effort: an
// AI client that errors on every call still produces a finished AuditOutput
// with code-generated fallback fixes and summary, and the audit succeeds.
func TestPipelineFallback_SummaryOnAIError(t *testing.T) {
	t.Parallel()
	cx := buildFixtureContext(t)
	p := &audit.Pipeline{
		Crawl: func(ctx context.Context, _ string) (audit.CheckContext, error) { return cx, nil },
		AI:    erroringAI{},
		Now:   func() time.Time { return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC) },
	}
	out, err := p.Run(context.Background(), audit.AuditInput{
		AuditID:  uuid.New(),
		TenantID: uuid.New(),
		StoreID:  uuid.New(),
		StoreURL: "https://acme.myshopify.com",
	})
	if err != nil {
		t.Fatalf("audit must succeed even when AI errors: %v", err)
	}
	if out.Summary == "" {
		t.Error("expected fallback summary")
	}
	// Every AI-eligible failed issue should have either an AI suggestion (none
	// here, since AI errored) or a code-generated fallback (which the pipeline
	// produces). Either way we expect the suggestion map populated.
	if hasAIEligibleFails(out) && len(out.Suggestions) == 0 {
		t.Error("expected fallback suggestions; got none")
	}
}

// TestPipelineRequiresValidate proves stage 1 is hard-required.
func TestPipelineRequiresValidate(t *testing.T) {
	t.Parallel()
	p := &audit.Pipeline{
		Crawl: func(ctx context.Context, _ string) (audit.CheckContext, error) { return audit.CheckContext{}, nil },
		AI:    ai.NewMockClient(),
		Now:   func() time.Time { return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC) },
	}
	_, err := p.Run(context.Background(), audit.AuditInput{}) // empty input → bad
	if err == nil {
		t.Fatal("expected validate to fail with empty input")
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// buildFixtureContext loads HTML fixtures and constructs a CheckContext
// directly — we already proved the crawler works in the existing integration
// test. This keeps the pipeline test fast and stable.
func buildFixtureContext(t *testing.T) audit.CheckContext {
	t.Helper()
	root := repoRoot(t)
	homepage := loadFixturePage(t, root, "homepage_with_contact.html", "https://acme.myshopify.com/")
	complete := loadFixturePage(t, root, "products/long_description.html", "https://acme.myshopify.com/products/long")
	missingBrand := loadFixturePage(t, root, "products/missing_brand_and_availability.html", "https://acme.myshopify.com/products/short")
	about := loadFixturePage(t, root, "pages/about_substantive.html", "https://acme.myshopify.com/pages/about")

	robotsBytes, err := os.ReadFile(filepath.Join(root, "testdata/shopify/robots/good.txt"))
	if err != nil {
		t.Fatal(err)
	}

	return audit.CheckContext{
		StoreURL:     "https://acme.myshopify.com",
		Homepage:     homepage,
		ProductPages: []*audit.Page{complete, missingBrand},
		PolicyPages:  map[string]*audit.Page{"about": about},
		SitemapURLs:  []string{"https://acme.myshopify.com/products/long", "https://acme.myshopify.com/products/short", "https://acme.myshopify.com/", "https://acme.myshopify.com/policies/refund-policy", "https://acme.myshopify.com/pages/about"},
		RobotsTxt:    string(robotsBytes),
	}
}

func repoRoot(t *testing.T) string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile = .../internal/audit/pipeline_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func loadFixturePage(t *testing.T, root, rel, urlForPage string) *audit.Page {
	t.Helper()
	full := filepath.Join(root, "testdata", "shopify", rel)
	b, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return &audit.Page{
		URL:        urlForPage,
		StatusCode: 200,
		HTML:       string(b),
		Doc:        doc,
		Headers:    http.Header{},
		FetchedAt:  time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
	}
}

// stripVolatile zeroes wall-clock fields that aren't part of the deterministic
// output contract — durations are non-deterministic across runs even with a
// fixed clock because the clock is sampled each Now() call.
func stripVolatile(out *audit.AuditOutput) {
	out.Duration = 0
	out.StartedAt = time.Time{}
	for i := range out.Stages {
		out.Stages[i].Started = time.Time{}
		out.Stages[i].Duration = 0
	}
	for i := range out.Results {
		out.Results[i].StartedAt = time.Time{}
		out.Results[i].Duration = 0
	}
}

func hasAIEligibleFails(out *audit.AuditOutput) bool {
	for _, r := range out.Results {
		if r.Status == audit.StatusFail && r.Meta.AIFixEligible && len(r.Issues) > 0 {
			return true
		}
	}
	return false
}

// erroringAI is an ai.Client that returns an error from every method.
type erroringAI struct{}

func (erroringAI) GenerateFix(_ context.Context, _ ai.FixRequest) (ai.FixResponse, error) {
	return ai.FixResponse{}, errFake
}
func (erroringAI) GenerateFixBatch(_ context.Context, _ ai.BatchFixRequest) ([]ai.FixResponse, error) {
	return nil, errFake
}
func (erroringAI) GenerateSummary(_ context.Context, _ ai.SummaryRequest) (ai.SummaryResponse, error) {
	return ai.SummaryResponse{}, errFake
}

var errFake = &fakeErr{}

type fakeErr struct{}

func (e *fakeErr) Error() string { return "ai: fake provider down" }
