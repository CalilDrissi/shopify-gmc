package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// Helper: an httptest server that records calls and lets the test stage
// per-call status codes.
// ----------------------------------------------------------------------------

type stagedServer struct {
	t        *testing.T
	mu       chan struct{}
	calls    int32
	statuses []int  // status code per attempt (cycles to last)
	bodies   []string
	gotAuth  []string
}

func newStagedServer(t *testing.T) *stagedServer { return &stagedServer{t: t, mu: make(chan struct{}, 1)} }

func (s *stagedServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&s.calls, 1) - 1
		auth := r.Header.Get("Authorization")
		s.gotAuth = append(s.gotAuth, auth)

		// Per-attempt status code; clamp to last entry.
		var status int
		if int(idx) >= len(s.statuses) {
			status = s.statuses[len(s.statuses)-1]
		} else {
			status = s.statuses[idx]
		}
		var body string
		if int(idx) >= len(s.bodies) {
			body = s.bodies[len(s.bodies)-1]
		} else {
			body = s.bodies[idx]
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func okBody(content string) string {
	r := chatResponse{
		Model:   "test-model",
		Choices: []chatChoice{{Message: chatMessage{Role: "assistant", Content: content}}},
		Usage:   chatUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func newClient(t *testing.T, baseURL string, opts ...Option) *OpenAIClient {
	t.Helper()
	settings := StaticSettings{BaseURL: baseURL, APIKey: "sk-test-secret", Model: "test-model"}
	defaults := []Option{
		WithMaxAttempts(3),
		WithBackoffBase(time.Millisecond), // make tests fast
		WithLogger(slog.New(slog.NewJSONHandler(io.Discard, nil))),
	}
	return NewOpenAIClient(settings, append(defaults, opts...)...)
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestGenerateFix_HappyPath(t *testing.T) {
	t.Parallel()
	s := newStagedServer(t)
	s.statuses = []int{200}
	s.bodies = []string{okBody("Use the brand-name vendor instead of the store name.")}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	c := newClient(t, srv.URL)
	res, err := c.GenerateFix(context.Background(), FixRequest{
		IssueID: "iss-1", CheckID: "product_schema_complete", Severity: "error",
		Title: "missing brand", Detail: "no brand field",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Suggested == "" || res.TokensIn == 0 {
		t.Errorf("empty result: %+v", res)
	}
	if !strings.HasPrefix(s.gotAuth[0], "Bearer sk-") {
		t.Errorf("auth header not set: %q", s.gotAuth[0])
	}
}

func TestRetries_OnRateLimitAndServerError(t *testing.T) {
	t.Parallel()
	s := newStagedServer(t)
	s.statuses = []int{429, 503, 200}
	s.bodies = []string{`{"error":{"message":"rate limited"}}`,
		`{"error":{"message":"upstream timeout"}}`,
		okBody("Done after retries.")}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	c := newClient(t, srv.URL)
	res, err := c.GenerateFix(context.Background(), FixRequest{IssueID: "x", CheckID: "k"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Suggested != "Done after retries." {
		t.Errorf("suggested = %q", res.Suggested)
	}
	if got := atomic.LoadInt32(&s.calls); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestNoRetry_On4xxOtherThan429(t *testing.T) {
	t.Parallel()
	s := newStagedServer(t)
	s.statuses = []int{401}
	s.bodies = []string{`{"error":{"message":"invalid api key","type":"auth_error"}}`}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	c := newClient(t, srv.URL)
	_, err := c.GenerateFix(context.Background(), FixRequest{IssueID: "x", CheckID: "k"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&s.calls); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retry)", got)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, expected 401 surfaced", err)
	}
}

func TestRetries_GiveUpAfterMax(t *testing.T) {
	t.Parallel()
	s := newStagedServer(t)
	s.statuses = []int{500}
	s.bodies = []string{`{"error":{"message":"oops"}}`}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	c := newClient(t, srv.URL, WithMaxAttempts(3))
	_, err := c.GenerateFix(context.Background(), FixRequest{IssueID: "x", CheckID: "k"})
	if err == nil {
		t.Fatal("expected failure after exhausting retries")
	}
	if got := atomic.LoadInt32(&s.calls); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestNoConfig_FailsImmediately(t *testing.T) {
	t.Parallel()
	c := NewOpenAIClient(StaticSettings{}) // empty
	_, err := c.GenerateFix(context.Background(), FixRequest{})
	if !errors.Is(err, ErrNoConfig) {
		t.Errorf("err = %v, want ErrNoConfig", err)
	}
}

func TestAPIKey_NeverLogged(t *testing.T) {
	t.Parallel()
	s := newStagedServer(t)
	s.statuses = []int{500, 500, 200}
	s.bodies = []string{`{"error":"oops"}`, `{"error":"oops"}`, okBody("ok")}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := newClient(t, srv.URL, WithLogger(logger))
	_, err := c.GenerateFix(context.Background(), FixRequest{IssueID: "x", CheckID: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "sk-test-secret") {
		t.Error("API key leaked into slog output")
	}
	if strings.Contains(buf.String(), "Bearer ") {
		t.Error("Bearer header leaked into slog output")
	}
}

func TestBatch_ParsesNumberedFixes(t *testing.T) {
	t.Parallel()
	s := newStagedServer(t)
	s.statuses = []int{200}
	s.bodies = []string{okBody(`<<FIX 1>>: First rewrite goes here.
<<FIX 2>>: Second rewrite goes here.
<<FIX 3>>: Third rewrite goes here.`)}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	c := newClient(t, srv.URL)
	resps, err := c.GenerateFixBatch(context.Background(), BatchFixRequest{
		Issues: []FixRequest{
			{IssueID: "a", CheckID: "k"}, {IssueID: "b", CheckID: "k"}, {IssueID: "c", CheckID: "k"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resps) != 3 {
		t.Fatalf("got %d", len(resps))
	}
	if !strings.HasPrefix(resps[0].Suggested, "First") ||
		!strings.HasPrefix(resps[1].Suggested, "Second") ||
		!strings.HasPrefix(resps[2].Suggested, "Third") {
		t.Errorf("parse mismatch: %+v", resps)
	}
}

func TestBatch_ExceedsLimit(t *testing.T) {
	t.Parallel()
	c := newClient(t, "http://nope")
	too := make([]FixRequest, 11)
	_, err := c.GenerateFixBatch(context.Background(), BatchFixRequest{Issues: too})
	if !errors.Is(err, ErrBatchTooLarge) {
		t.Errorf("err = %v, want ErrBatchTooLarge", err)
	}
}

func TestSummary_ParsesShape(t *testing.T) {
	t.Parallel()
	s := newStagedServer(t)
	s.statuses = []int{200}
	s.bodies = []string{okBody(`SUMMARY: This store is close to GMC-ready.
NEXT STEPS:
- Fix the privacy policy link
- Add a real shipping policy
- Replace ALL CAPS titles`)}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	c := newClient(t, srv.URL)
	res, err := c.GenerateSummary(context.Background(), SummaryRequest{
		StoreName: "Acme", StoreURL: "https://acme.example", Score: 78, RiskLevel: "medium",
		IssueCounts: map[string]int{"error": 1, "warning": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Summary, "This store is close") {
		t.Errorf("summary = %q", res.Summary)
	}
	if len(res.NextSteps) != 3 {
		t.Errorf("steps = %d, want 3 (%+v)", len(res.NextSteps), res.NextSteps)
	}
}

func TestBudget(t *testing.T) {
	t.Parallel()
	b := NewBudget(2)
	for i := 0; i < 2; i++ {
		if err := b.Use(); err != nil {
			t.Fatalf("use #%d: %v", i, err)
		}
	}
	if err := b.Use(); !errors.Is(err, ErrBudgetExceeded) {
		t.Errorf("expected ErrBudgetExceeded, got %v", err)
	}
}

func TestMockClient_Determinism(t *testing.T) {
	t.Parallel()
	m := NewMockClient()
	req := FixRequest{IssueID: "iss-1", CheckID: "product_schema_complete", Severity: "error"}
	a, _ := m.GenerateFix(context.Background(), req)
	b, _ := m.GenerateFix(context.Background(), req)
	if a.Suggested != b.Suggested {
		t.Errorf("mock not deterministic: %q vs %q", a.Suggested, b.Suggested)
	}
}
