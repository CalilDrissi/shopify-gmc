package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/ai"
)

// ----------------------------------------------------------------------------
// Inputs / outputs / stage types
// ----------------------------------------------------------------------------

// CrawlFn lets callers swap the live crawler for a fixture in tests.
// Signature mirrors crawler.Crawler.Crawl after constructing one for the URL.
type CrawlFn func(ctx context.Context, storeURL string) (CheckContext, error)

// GMCSyncFn fetches Google Merchant Center data for a store. Returns
// (nil, nil) when the store isn't connected to GMC — that's a normal
// "skip this stage" outcome, not an error. Returns (nil, err) when the
// connection exists but the API call failed; the pipeline logs and proceeds
// with crawler-only data, so audits don't fail because GMC is flapping.
type GMCSyncFn func(ctx context.Context, in AuditInput) (*GMCContext, error)

// Persister writes the final audit + issues. The pipeline calls Save once
// at the end of stage 7. We use an interface so tests can verify without
// hitting Postgres.
type Persister interface {
	Save(ctx context.Context, in AuditInput, out *AuditOutput) error
}

type AuditInput struct {
	AuditID      uuid.UUID
	TenantID     uuid.UUID
	StoreID      uuid.UUID
	TriggeredBy  *uuid.UUID
	StoreURL     string
	StoreName    string
	StoreContext string
	Trigger      string // "manual", "scheduled", "webhook"
}

type CategorySummary struct {
	Category    string
	Pass, Fail  int
	WorstStatus Status
	WorstSeverity Severity
}

type AuditOutput struct {
	AuditID    uuid.UUID
	Score      int
	RiskLevel  string
	Counts     map[string]int // by severity
	Categories []CategorySummary
	Results    []CheckResult
	Suggestions map[string]string // keyed by issue index "ck.id#N"
	Summary    string
	NextSteps  []string
	Stages     []StageResult
	StartedAt  time.Time
	Duration   time.Duration
}

type StageResult struct {
	Name     string
	Started  time.Time
	Duration time.Duration
	Status   string // "ok", "skipped", "failed"
	Detail   string
}

// ----------------------------------------------------------------------------
// Pipeline
// ----------------------------------------------------------------------------

// ProgressSink is called between stages so the live HTMX progress page can
// poll for an up-to-date stage list while the audit is still running.
type ProgressSink interface {
	SaveProgress(ctx context.Context, auditID string, stages []StageResult, status string) error
}

type Pipeline struct {
	Crawl     CrawlFn
	GMC       GMCSyncFn
	AI        ai.Client
	Persist   Persister
	Progress  ProgressSink
	Logger    *slog.Logger
	Now       func() time.Time
	Budget    int  // per-audit AI call cap; 0 → use 50
	BatchSize int  // issues per batched fix call; 0 → use 10
}

const (
	defaultBudget    = 50
	defaultBatchSize = 10
)

// Run executes all 7 stages. Stages 1-4 + 7 are required; 5 and 6 are
// best-effort. The returned error is non-nil only when a required stage
// failed; the AuditOutput is populated as far as the pipeline got.
func (p *Pipeline) Run(ctx context.Context, in AuditInput) (*AuditOutput, error) {
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Logger == nil {
		p.Logger = slog.Default()
	}
	budget := ai.NewBudget(coalesce(p.Budget, defaultBudget))
	batchSize := coalesce(p.BatchSize, defaultBatchSize)

	out := &AuditOutput{
		AuditID:     in.AuditID,
		Counts:      map[string]int{},
		Suggestions: map[string]string{},
		StartedAt:   p.Now(),
	}

	// Stage 1: Validate
	cx, err := p.runValidate(ctx, in, out)
	if err != nil {
		out.Duration = p.Now().Sub(out.StartedAt)
		_ = p.persistFailed(ctx, in, out, err)
		return out, err
	}

	// Stage 2: Crawl
	cx, err = p.runCrawl(ctx, in, out)
	if err != nil {
		out.Duration = p.Now().Sub(out.StartedAt)
		_ = p.persistFailed(ctx, in, out, err)
		return out, err
	}

	// Stage 2.5: GMC sync (best-effort).
	// Runs between Crawl and Detect so the GMC-native checks have data
	// when Detect iterates the registry. Failure here never fails the
	// audit — the crawler-side data still produces a valid score.
	p.runGMCSync(ctx, in, &cx, out)

	// Stage 3: Detect
	if err := p.runDetect(ctx, cx, out); err != nil {
		out.Duration = p.Now().Sub(out.StartedAt)
		_ = p.persistFailed(ctx, in, out, err)
		return out, err
	}

	// Stage 4: Score
	if err := p.runScore(ctx, out); err != nil {
		out.Duration = p.Now().Sub(out.StartedAt)
		_ = p.persistFailed(ctx, in, out, err)
		return out, err
	}

	// Stage 5: Enrich (best-effort)
	p.runEnrich(ctx, in, budget, batchSize, out)

	// Stage 6: Summarize (best-effort)
	p.runSummarize(ctx, in, budget, out)

	// Stage 7: Persist (required)
	out.Duration = p.Now().Sub(out.StartedAt)
	if err := p.runPersist(ctx, in, out); err != nil {
		return out, err
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// Stage 1: Validate
// ----------------------------------------------------------------------------

func (p *Pipeline) runValidate(ctx context.Context, in AuditInput, out *AuditOutput) (CheckContext, error) {
	stage := p.beginStage("validate")
	if in.StoreURL == "" {
		return CheckContext{}, p.failStage(out, stage, "store URL is empty")
	}
	if in.StoreID == uuid.Nil || in.TenantID == uuid.Nil {
		return CheckContext{}, p.failStage(out, stage, "missing IDs")
	}
	p.endStage(out, stage, "ok", "")
	return CheckContext{StoreURL: in.StoreURL}, nil
}

// ----------------------------------------------------------------------------
// Stage 2: Crawl
// ----------------------------------------------------------------------------

func (p *Pipeline) runCrawl(ctx context.Context, in AuditInput, out *AuditOutput) (CheckContext, error) {
	stage := p.beginStage("crawl")
	if p.Crawl == nil {
		return CheckContext{}, p.failStage(out, stage, "no crawler configured")
	}
	cx, err := p.Crawl(ctx, in.StoreURL)
	if err != nil {
		return CheckContext{}, p.failStage(out, stage, err.Error())
	}
	p.endStage(out, stage, "ok",
		fmt.Sprintf("homepage + %d products + %d collections + %d policies",
			len(cx.ProductPages), len(cx.CollectionPages), len(cx.PolicyPages)))
	return cx, nil
}

// ----------------------------------------------------------------------------
// Stage 2.5: GMC sync — best-effort
// ----------------------------------------------------------------------------

func (p *Pipeline) runGMCSync(ctx context.Context, in AuditInput, cx *CheckContext, out *AuditOutput) {
	stage := p.beginStage("gmc_sync")
	if p.GMC == nil {
		p.endStage(out, stage, "skipped", "no GMC sync configured")
		return
	}
	gx, err := p.GMC(ctx, in)
	if err != nil {
		// Log + continue. Do not fail the audit just because Google's API
		// is flapping or our token expired — checks that depend on GMC
		// will silently no-op when cx.GMC is nil.
		p.Logger.Warn("gmc_sync_failed",
			slog.String("store_id", in.StoreID.String()),
			slog.Any("err", err))
		p.endStage(out, stage, "ok", "skipped: "+err.Error())
		return
	}
	if gx == nil {
		p.endStage(out, stage, "skipped", "store not connected to GMC")
		return
	}
	cx.GMC = gx
	prods := len(gx.Products)
	feeds := len(gx.Feeds)
	acctIssues := 0
	if gx.Account != nil {
		acctIssues = len(gx.Account.AccountLevelIssues)
	}
	p.endStage(out, stage, "ok",
		fmt.Sprintf("merchant=%s product_statuses=%d feeds=%d account_issues=%d",
			gx.MerchantID, prods, feeds, acctIssues))
}

// ----------------------------------------------------------------------------
// Stage 3: Detect — run all registered checks
// ----------------------------------------------------------------------------

func (p *Pipeline) runDetect(ctx context.Context, cx CheckContext, out *AuditOutput) error {
	stage := p.beginStage("detect")
	checks := All()
	if len(checks) == 0 {
		return p.failStage(out, stage, "no checks registered")
	}
	results := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		results = append(results, c.Run(ctx, cx))
	}
	out.Results = results
	p.endStage(out, stage, "ok", fmt.Sprintf("ran %d checks", len(checks)))
	return nil
}

// ----------------------------------------------------------------------------
// Stage 4: Score — weighted sum + risk level + per-category roll-up
// ----------------------------------------------------------------------------

const (
	weightCritical = 10
	weightError    = 5
	weightWarning  = 2
	weightInfo     = 0
)

func (p *Pipeline) runScore(_ context.Context, out *AuditOutput) error {
	stage := p.beginStage("score")
	score := 100
	counts := map[string]int{"critical": 0, "error": 0, "warning": 0, "info": 0}
	cats := map[string]*CategorySummary{}

	for _, r := range out.Results {
		cat, ok := cats[r.Meta.Category]
		if !ok {
			cat = &CategorySummary{Category: r.Meta.Category, WorstStatus: StatusPass, WorstSeverity: SeverityInfo}
			cats[r.Meta.Category] = cat
		}
		switch r.Status {
		case StatusPass:
			cat.Pass++
		case StatusFail:
			cat.Fail++
			cat.WorstStatus = StatusFail
			if severityRank(r.Severity) > severityRank(cat.WorstSeverity) {
				cat.WorstSeverity = r.Severity
			}
			perIssue := weightFor(r.Severity)
			score -= perIssue * len(r.Issues)
			counts[string(r.Severity)] += len(r.Issues)
		case StatusInfo:
			counts["info"] += len(r.Issues)
		}
	}
	if score < 0 {
		score = 0
	}

	out.Score = score
	out.Counts = counts
	out.RiskLevel = riskLevel(score)
	out.Categories = make([]CategorySummary, 0, len(cats))
	for _, c := range cats {
		out.Categories = append(out.Categories, *c)
	}
	sort.Slice(out.Categories, func(i, j int) bool {
		return out.Categories[i].Category < out.Categories[j].Category
	})
	p.endStage(out, stage, "ok", fmt.Sprintf("score=%d risk=%s", score, out.RiskLevel))
	return nil
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

func weightFor(s Severity) int {
	switch s {
	case SeverityCritical:
		return weightCritical
	case SeverityError:
		return weightError
	case SeverityWarning:
		return weightWarning
	}
	return weightInfo
}

func riskLevel(score int) string {
	switch {
	case score >= 80:
		return "low"
	case score >= 50:
		return "medium"
	default:
		return "high"
	}
}

// ----------------------------------------------------------------------------
// Stage 5: Enrich (best-effort) — AI fix suggestions for AIFixEligible issues
// ----------------------------------------------------------------------------

func (p *Pipeline) runEnrich(ctx context.Context, in AuditInput, budget *ai.Budget, batchSize int, out *AuditOutput) {
	stage := p.beginStage("enrich")
	if p.AI == nil {
		p.endStage(out, stage, "skipped", "no AI client configured")
		return
	}
	type lookup struct {
		key string
		ai.FixRequest
	}
	var todo []lookup
	for _, r := range out.Results {
		if r.Status != StatusFail || !r.Meta.AIFixEligible {
			continue
		}
		for i, iss := range r.Issues {
			key := fmt.Sprintf("%s#%d", r.Meta.ID, i)
			todo = append(todo, lookup{
				key: key,
				FixRequest: ai.FixRequest{
					IssueID:      key,
					CheckID:      r.Meta.ID,
					Severity:     string(r.Severity),
					Title:        r.Meta.Title,
					Detail:       iss.Detail,
					Evidence:     iss.Evidence,
					ProductTitle: iss.ProductTitle,
					ProductURL:   iss.URL,
					StoreContext: in.StoreContext,
				},
			})
		}
	}
	if len(todo) == 0 {
		p.endStage(out, stage, "ok", "no AI-eligible issues")
		return
	}

	calls, hits, fails := 0, 0, 0
	for start := 0; start < len(todo); start += batchSize {
		end := start + batchSize
		if end > len(todo) {
			end = len(todo)
		}
		// Budget check before each call.
		if err := budget.Use(); err != nil {
			p.Logger.Warn("ai_budget_exhausted", slog.Int("done", start), slog.Int("remaining", len(todo)-start))
			break
		}
		calls++
		batch := todo[start:end]
		req := ai.BatchFixRequest{StoreContext: in.StoreContext}
		for _, t := range batch {
			req.Issues = append(req.Issues, t.FixRequest)
		}
		resps, err := p.AI.GenerateFixBatch(ctx, req)
		if err != nil {
			fails++
			// Fallback: code-generated sentence per issue.
			for _, t := range batch {
				out.Suggestions[t.key] = fallbackFix(t.FixRequest)
			}
			continue
		}
		for i, r := range resps {
			if i >= len(batch) {
				break
			}
			s := r.Suggested
			if s == "" || s == "(no rewrite produced)" {
				s = fallbackFix(batch[i].FixRequest)
				fails++
			} else {
				hits++
			}
			out.Suggestions[batch[i].key] = s
		}
	}
	p.endStage(out, stage, "ok",
		fmt.Sprintf("calls=%d hits=%d fallbacks=%d", calls, hits, fails))
}

func fallbackFix(req ai.FixRequest) string {
	return fmt.Sprintf("Apply the steps in the \"How to apply\" panel below to resolve %s for %s.",
		req.CheckID, coalesce(req.ProductTitle, "this item"))
}

// ----------------------------------------------------------------------------
// Stage 6: Summarize (best-effort)
// ----------------------------------------------------------------------------

func (p *Pipeline) runSummarize(ctx context.Context, in AuditInput, budget *ai.Budget, out *AuditOutput) {
	stage := p.beginStage("summarize")
	if p.AI == nil {
		out.Summary = fallbackSummary(out)
		p.endStage(out, stage, "skipped", "no AI client configured")
		return
	}
	if err := budget.Use(); err != nil {
		out.Summary = fallbackSummary(out)
		p.endStage(out, stage, "ok", "fallback (budget exhausted)")
		return
	}
	req := ai.SummaryRequest{
		StoreURL:     in.StoreURL,
		StoreName:    in.StoreName,
		Score:        out.Score,
		RiskLevel:    out.RiskLevel,
		IssueCounts:  map[string]int{"critical": out.Counts["critical"], "error": out.Counts["error"], "warning": out.Counts["warning"], "info": out.Counts["info"]},
		StoreContext: in.StoreContext,
		TopIssues:    topIssuesForSummary(out, 8),
	}
	resp, err := p.AI.GenerateSummary(ctx, req)
	if err != nil {
		out.Summary = fallbackSummary(out)
		p.endStage(out, stage, "ok", "fallback (ai error: "+err.Error()+")")
		return
	}
	out.Summary = resp.Summary
	out.NextSteps = resp.NextSteps
	p.endStage(out, stage, "ok", fmt.Sprintf("tokens_in=%d tokens_out=%d", resp.TokensIn, resp.TokensOut))
}

func fallbackSummary(out *AuditOutput) string {
	c, e, w := out.Counts["critical"], out.Counts["error"], out.Counts["warning"]
	switch {
	case c > 0:
		return fmt.Sprintf("Audit complete. %d critical, %d error and %d warning issues; risk level %s. Address critical items first.", c, e, w, out.RiskLevel)
	case e > 0:
		return fmt.Sprintf("Audit complete. %d error and %d warning issues; risk level %s. Fix the error items before submitting the GMC feed.", e, w, out.RiskLevel)
	case w > 0:
		return fmt.Sprintf("Audit complete. %d warning issues; risk level %s. No blockers — polish at your pace.", w, out.RiskLevel)
	default:
		return fmt.Sprintf("Audit complete. No blocking issues; risk level %s.", out.RiskLevel)
	}
}

func topIssuesForSummary(out *AuditOutput, max int) []ai.FixRequest {
	var top []ai.FixRequest
	for _, r := range out.Results {
		if r.Status != StatusFail {
			continue
		}
		for _, iss := range r.Issues {
			top = append(top, ai.FixRequest{
				CheckID:  r.Meta.ID,
				Severity: string(r.Severity),
				Title:    r.Meta.Title,
				Detail:   iss.Detail,
			})
			if len(top) >= max {
				return top
			}
		}
	}
	return top
}

// ----------------------------------------------------------------------------
// Stage 7: Persist (required)
// ----------------------------------------------------------------------------

func (p *Pipeline) runPersist(ctx context.Context, in AuditInput, out *AuditOutput) error {
	stage := p.beginStage("persist")
	if p.Persist == nil {
		p.endStage(out, stage, "skipped", "no persister configured (in-memory)")
		return nil
	}
	if err := p.Persist.Save(ctx, in, out); err != nil {
		return p.failStage(out, stage, err.Error())
	}
	p.endStage(out, stage, "ok", "")
	return nil
}

func (p *Pipeline) persistFailed(ctx context.Context, in AuditInput, out *AuditOutput, runErr error) error {
	if p.Persist == nil {
		return nil
	}
	out.Summary = "Audit failed: " + runErr.Error()
	return p.Persist.Save(ctx, in, out)
}

// ----------------------------------------------------------------------------
// Stage helpers
// ----------------------------------------------------------------------------

func (p *Pipeline) beginStage(name string) StageResult {
	return StageResult{Name: name, Started: p.Now()}
}

func (p *Pipeline) endStage(out *AuditOutput, st StageResult, status, detail string) {
	st.Status = status
	st.Detail = detail
	st.Duration = p.Now().Sub(st.Started)
	defer p.flushProgress(out)
	out.Stages = append(out.Stages, st)
	p.Logger.Info("stage_done",
		slog.String("stage", st.Name),
		slog.String("status", st.Status),
		slog.String("detail", st.Detail),
		slog.Duration("dur", st.Duration),
	)
}

func (p *Pipeline) failStage(out *AuditOutput, st StageResult, detail string) error {
	st.Status = "failed"
	st.Detail = detail
	st.Duration = p.Now().Sub(st.Started)
	out.Stages = append(out.Stages, st)
	p.Logger.Warn("stage_failed", slog.String("stage", st.Name), slog.String("detail", detail))
	return errors.New(detail)
}

// flushProgress fires whenever a stage closes, so the live HTMX page can
// reflect the latest state. Best-effort: errors are logged and swallowed.
func (p *Pipeline) flushProgress(out *AuditOutput) {
	if p.Progress == nil || out == nil {
		return
	}
	if err := p.Progress.SaveProgress(context.Background(), out.AuditID.String(), out.Stages, "running"); err != nil {
		p.Logger.Warn("save_progress", slog.Any("err", err))
	}
}

func coalesce[T comparable](a, fallback T) T {
	var zero T
	if a == zero {
		return fallback
	}
	return a
}
