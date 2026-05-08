// Package ai provides a small, provider-agnostic client over an
// OpenAI-compatible Chat Completions endpoint, plus a deterministic mock
// implementation used by tests and the audit pipeline's "mocked AI" mode.
//
// Design constraints:
//   - stdlib net/http; no SDK
//   - Bearer token auth; api_key is read from internal/settings (5-min cache)
//   - 60s timeout, 3 retries on 429 + 5xx with exponential backoff
//   - No retry on other 4xx (auth, request shape, etc.)
//   - 50 AI calls per audit (Budget enforced by the pipeline)
//   - Provider-agnostic: plain text in, plain text out — no Claude tool use,
//     no OpenAI JSON mode, no function calling
//   - Logs provider/model/token counts via slog; never logs the API key
package ai

import (
	"context"
	"errors"
)

// ----------------------------------------------------------------------------
// Public types — the surface the audit pipeline depends on.
// ----------------------------------------------------------------------------

type Client interface {
	GenerateFix(ctx context.Context, req FixRequest) (FixResponse, error)
	GenerateFixBatch(ctx context.Context, req BatchFixRequest) ([]FixResponse, error)
	GenerateSummary(ctx context.Context, req SummaryRequest) (SummaryResponse, error)
}

// FixRequest carries everything the model needs to suggest a single rewrite.
type FixRequest struct {
	IssueID      string // stable identifier (used by mock for determinism)
	CheckID      string // e.g. "product_schema_complete"
	Severity     string
	Title        string
	Detail       string
	Evidence     string
	ProductTitle string
	ProductURL   string
	StoreContext string // optional brand description
}

type FixResponse struct {
	IssueID    string
	Suggested  string
	Reasoning  string
	TokensIn   int
	TokensOut  int
	ModelUsed  string
}

// BatchFixRequest groups up to 10 issues into a single call. The pipeline is
// responsible for splitting larger groups.
type BatchFixRequest struct {
	Issues       []FixRequest
	StoreContext string
}

type SummaryRequest struct {
	StoreURL     string
	StoreName    string
	Score        int
	RiskLevel    string
	IssueCounts  map[string]int // keys: critical, error, warning, info
	TopIssues    []FixRequest
	StoreContext string
}

type SummaryResponse struct {
	Summary   string
	NextSteps []string
	TokensIn  int
	TokensOut int
	ModelUsed string
}

// ----------------------------------------------------------------------------
// Errors
// ----------------------------------------------------------------------------

var (
	ErrBudgetExceeded = errors.New("ai: per-audit budget exhausted")
	ErrNoConfig       = errors.New("ai: provider not configured (base_url + api_key + model required)")
	ErrBatchTooLarge  = errors.New("ai: batch exceeds 10 issues")
	ErrEmptyResponse  = errors.New("ai: provider returned an empty response")
)

// ----------------------------------------------------------------------------
// Budget — per-audit cap. Pipeline creates one Budget and shares it across
// every AI call within the audit; AI client doesn't know about budgets.
// ----------------------------------------------------------------------------

type Budget struct {
	Limit int
	used  int
}

func NewBudget(limit int) *Budget { return &Budget{Limit: limit} }

// Use deducts a single call from the budget. Returns ErrBudgetExceeded when
// the budget is empty.
func (b *Budget) Use() error {
	if b == nil {
		return nil
	}
	if b.used >= b.Limit {
		return ErrBudgetExceeded
	}
	b.used++
	return nil
}

// Used returns the number of calls already deducted.
func (b *Budget) Used() int {
	if b == nil {
		return 0
	}
	return b.used
}

// Remaining is convenience for templates / logs.
func (b *Budget) Remaining() int {
	if b == nil {
		return 0
	}
	return b.Limit - b.used
}
