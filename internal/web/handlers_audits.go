package web

import (
	"bytes"
	"context"
	stdBase64 "encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/audit"
	_ "github.com/example/gmcauditor/internal/audit/checks"
	"github.com/example/gmcauditor/internal/audit/differ"
)

// EnqueueAudit creates a new audits row at status='queued' and pushes a
// matching audit_jobs entry. The worker picks it up on the next 2s tick.
func (h *Handlers) EnqueueAudit(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	storeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad store id", http.StatusBadRequest)
		return
	}
	q, _ := QuerierFromContext(r.Context())

	s, err := h.Store.Stores.GetByID(r.Context(), q, d.Tenant.ID, storeID)
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Store not found.")
		return
	}
	if e := h.EnforcePlanLimit(r.Context(), d.Tenant.ID, string(d.Tenant.Plan), ResAuditsMonth); e != nil {
		h.RenderPlanLimit(w, r, e)
		return
	}

	auditID := uuid.New()
	storeURL := StoreURLFor(s.ShopDomain)
	storeName := s.ShopDomain
	if s.DisplayName != nil && *s.DisplayName != "" {
		storeName = *s.DisplayName
	}
	triggeredBy := &d.User.ID

	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		h.Logger.Error("enqueue begin", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not start audit.")
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO audits (id, tenant_id, store_id, triggered_by, trigger, status, started_at, issue_counts, progress)
		VALUES ($1, $2, $3, $4, 'manual', 'queued', NULL, '{}'::jsonb, '[]'::jsonb)
	`, auditID, d.Tenant.ID, s.ID, triggeredBy); err != nil {
		h.Logger.Error("enqueue insert audit", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not start audit.")
		return
	}

	payload := map[string]any{
		"audit_id":     auditID,
		"tenant_id":    d.Tenant.ID,
		"store_id":     s.ID,
		"store_url":    storeURL,
		"store_name":   storeName,
		"trigger":      "manual",
		"triggered_by": triggeredBy,
	}
	payloadBytes, _ := json.Marshal(payload)

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO audit_jobs (tenant_id, kind, payload, run_at)
		VALUES ($1, 'audit_store', $2, now())
	`, d.Tenant.ID, payloadBytes); err != nil {
		h.Logger.Error("enqueue insert job", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not enqueue audit.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		h.Logger.Error("enqueue commit", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not save audit.")
		return
	}
	h.IncrementUsage(r.Context(), d.Tenant.ID, "audits")

	http.Redirect(w, r, fmt.Sprintf("/t/%s/audits/%s", d.Tenant.Slug, auditID), http.StatusFound)
}

// ----------------------------------------------------------------------------
// Live progress page + HTMX fragment
// ----------------------------------------------------------------------------

type auditView struct {
	ID           uuid.UUID
	StoreID      uuid.UUID
	StoreDomain  string
	StoreName    string
	Status       string
	Score        *int
	RiskLevel    *string
	Summary      *string
	NextSteps    []string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	IssueCounts  map[string]int
	Stages       []audit.StageResult
	Trigger      string
	ErrorMessage *string

	IssueGroups []issueGroup
	IssueCount  int
	Diff        *diffSummary

	// GMC tab data — populated when an active GMC connection exists for
	// the audit's store. nil → render the report without tabs.
	GMC *auditGMCView
}

type auditGMCView struct {
	MerchantID       string
	AccountStatus    string
	WebsiteClaimed   *bool
	WarningsCount    int
	SuspensionsCount int
	ProductCount     int
	CrawlerGroups    []issueGroup
	CrawlerCount     int
	GMCGroups        []issueGroup
	GMCCount         int
	Timeline         []gmcTimelineEntry
}

type gmcTimelineEntry struct {
	CapturedAt       time.Time
	AccountStatus    string
	WarningsCount    int
	SuspensionsCount int
	AuditID          *uuid.UUID
}

type issueGroup struct {
	Category string
	Issues   []issueView
}

type issueView struct {
	ID            uuid.UUID
	Severity      string
	RuleCode      string
	Title         string
	ProductTitle  string
	URL           string
	Detail        string
	Suggestion    string
	WhyItMatters  string
	DocsURL       string
	Difficulty    string
	TimeEstimate  string
	Steps         []audit.Step
	FixAppliedAt  *time.Time
	Source        string // "crawler" | "gmc_api"
	ExternalCode  string
}

func (h *Handlers) loadAudit(ctx context.Context, tenantID, auditID uuid.UUID) (*auditView, error) {
	var (
		v                auditView
		nextStepsBytes   []byte
		issueCountsBytes []byte
		progressBytes    []byte
	)
	err := h.Pool.QueryRow(ctx, `
		SELECT a.id, a.store_id, s.shop_domain, COALESCE(s.display_name, s.shop_domain),
		       a.status::text, a.score, a.risk_level, a.summary, a.next_steps,
		       a.started_at, a.finished_at, a.issue_counts, a.progress, a.trigger, a.error_message
		FROM audits a
		JOIN stores s ON s.id = a.store_id
		WHERE a.tenant_id=$1 AND a.id=$2
	`, tenantID, auditID).Scan(
		&v.ID, &v.StoreID, &v.StoreDomain, &v.StoreName,
		&v.Status, &v.Score, &v.RiskLevel, &v.Summary, &nextStepsBytes,
		&v.StartedAt, &v.FinishedAt, &issueCountsBytes, &progressBytes, &v.Trigger, &v.ErrorMessage,
	)
	if err != nil {
		return nil, err
	}
	if len(nextStepsBytes) > 0 {
		_ = json.Unmarshal(nextStepsBytes, &v.NextSteps)
	}
	if len(issueCountsBytes) > 0 {
		_ = json.Unmarshal(issueCountsBytes, &v.IssueCounts)
	}
	if len(progressBytes) > 0 {
		_ = json.Unmarshal(progressBytes, &v.Stages)
	}
	return &v, nil
}

// loadIssues fetches every issue for this audit and returns them grouped by
// category and sorted by severity (critical → error → warning → info).
func (h *Handlers) loadIssues(ctx context.Context, tenantID, auditID uuid.UUID) ([]issueGroup, int, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT id, severity::text, rule_code, title, COALESCE(product_title,''), COALESCE(product_id,''),
		       COALESCE(description,''), COALESCE(fix_instructions,''), fix_payload, fix_applied_at,
		       COALESCE(source,'crawler'), COALESCE(external_issue_code,'')
		FROM issues
		WHERE tenant_id=$1 AND audit_id=$2
	`, tenantID, auditID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	type rawIssue struct {
		issueView
		category string
	}
	var raws []rawIssue
	for rows.Next() {
		var (
			rv      rawIssue
			payload []byte
		)
		if err := rows.Scan(&rv.ID, &rv.Severity, &rv.RuleCode, &rv.Title, &rv.ProductTitle, &rv.URL,
			&rv.Detail, &rv.Suggestion, &payload, &rv.FixAppliedAt,
			&rv.Source, &rv.ExternalCode); err != nil {
			continue
		}
		var p struct {
			Meta         audit.Meta            `json:"meta"`
			Instructions audit.FixInstructions `json:"instructions"`
			Why          string                `json:"why"`
			DocsURL      string                `json:"docs_url"`
			Category     string                `json:"category"`
		}
		_ = json.Unmarshal(payload, &p)
		rv.category = p.Category
		if rv.category == "" {
			rv.category = "general"
		}
		rv.WhyItMatters = p.Why
		if rv.WhyItMatters == "" {
			rv.WhyItMatters = p.Instructions.WhyItMatters
		}
		rv.DocsURL = p.DocsURL
		rv.Difficulty = string(p.Instructions.Difficulty)
		rv.TimeEstimate = p.Instructions.TimeEstimate
		rv.Steps = p.Instructions.Steps
		raws = append(raws, rv)
	}

	sevRank := map[string]int{"critical": 4, "error": 3, "warning": 2, "info": 1}
	groups := map[string]*issueGroup{}
	order := []string{}
	for _, r := range raws {
		g, ok := groups[r.category]
		if !ok {
			g = &issueGroup{Category: r.category}
			groups[r.category] = g
			order = append(order, r.category)
		}
		g.Issues = append(g.Issues, r.issueView)
	}
	sort.Strings(order)
	out := make([]issueGroup, 0, len(order))
	total := 0
	for _, cat := range order {
		g := groups[cat]
		sort.SliceStable(g.Issues, func(i, j int) bool {
			ri, rj := sevRank[g.Issues[i].Severity], sevRank[g.Issues[j].Severity]
			if ri != rj {
				return ri > rj
			}
			return g.Issues[i].Title < g.Issues[j].Title
		})
		total += len(g.Issues)
		out = append(out, *g)
	}
	return out, total, nil
}

// AuditDetail renders the live progress page, and once succeeded, the full
// report (gauge + grouped issues + AI fix + copy-to-clipboard + resolve).
func (h *Handlers) AuditDetail(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	auditID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Audit not found.")
		return
	}
	v, err := h.loadAudit(r.Context(), d.Tenant.ID, auditID)
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Audit not found.")
		return
	}
	if v.Status == "succeeded" {
		groups, total, err := h.loadIssues(r.Context(), d.Tenant.ID, auditID)
		if err == nil {
			v.IssueGroups = groups
			v.IssueCount = total
		}
		v.Diff = h.loadAuditDiff(r.Context(), d.Tenant.ID, auditID)
		v.GMC = h.loadAuditGMC(r.Context(), d.Tenant.ID, v.StoreID, auditID, groups)
	}
	d.Title = "Audit"
	d.Data = v
	h.renderTenant(w, r, "audit-detail", d)
}

// AuditProgressFragment is the HTMX-polled HTML fragment that renders just
// the stage list + score summary card. Polled every 2s by audit-detail.html.
func (h *Handlers) AuditProgressFragment(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	auditID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	v, err := h.loadAudit(r.Context(), d.Tenant.ID, auditID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	t, err := h.Renderer.Page("audit-detail")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if v.Status == "succeeded" || v.Status == "failed" {
		// HX-Refresh tells HTMX to do a full reload — we want the report,
		// not just the progress fragment, once the audit is done.
		w.Header().Set("HX-Refresh", "true")
	}
	if err := t.ExecuteTemplate(w, "audit-progress-fragment", map[string]any{
		"Tenant": d.Tenant, "Audit": v, "CSRFToken": d.CSRFToken,
	}); err != nil {
		h.Logger.Error("render fragment", slog.Any("err", err))
	}
}

// loadAuditDiff fetches the audit_diffs row for this audit, returning nil
// if the audit was the first ever for the store (no previous to compare).
func (h *Handlers) loadAuditDiff(ctx context.Context, tenantID, auditID uuid.UUID) *diffSummary {
	var (
		newCount, resolvedCount, newCritical, scoreDelta int
		havePrev                                          bool
		diffJSON                                          []byte
	)
	err := h.Pool.QueryRow(ctx, `
		SELECT new_issue_count, resolved_issue_count, new_critical_count,
		       COALESCE(score_delta, 0),
		       previous_audit_id IS NOT NULL,
		       diff
		FROM audit_diffs
		WHERE tenant_id=$1 AND audit_id=$2
	`, tenantID, auditID).Scan(&newCount, &resolvedCount, &newCritical, &scoreDelta, &havePrev, &diffJSON)
	if err != nil {
		return nil
	}
	if !havePrev {
		return nil
	}
	var d differ.Diff
	_ = json.Unmarshal(diffJSON, &d)
	return &diffSummary{
		NewCount:         newCount,
		ResolvedCount:    resolvedCount,
		NewCriticalCount: newCritical,
		ScoreDelta:       scoreDelta,
		HavePrev:         havePrev,
		NewIssues:        d.NewIssues,
	}
}

// loadAuditGMC builds the tabbed view payload for an audit. Returns nil
// when there's no active GMC connection for the store and the audit has
// no GMC-sourced data — the report renders without tabs in that case.
//
// The crawler/gmc split is computed by partitioning the already-loaded
// `groups` by issue.Source so we don't re-query.
func (h *Handlers) loadAuditGMC(ctx context.Context, tenantID, storeID, auditID uuid.UUID, allGroups []issueGroup) *auditGMCView {
	// Snapshot for this audit (per-audit row written by the worker).
	var (
		acctStatus       string
		websiteClaimed   *bool
		warnings, susp   int
		productCount     int
	)
	err := h.Pool.QueryRow(ctx, `
		SELECT COALESCE(account_status,''), website_claimed,
		       COALESCE(warnings_count,0), COALESCE(suspensions_count,0),
		       COALESCE(product_count,0)
		FROM gmc_account_snapshots
		WHERE tenant_id = $1 AND audit_id = $2
		ORDER BY captured_at DESC
		LIMIT 1
	`, tenantID, auditID).Scan(&acctStatus, &websiteClaimed, &warnings, &susp, &productCount)
	hasSnapshot := err == nil

	// Partition issue groups by source. An issueGroup whose name starts
	// with "gmc" or whose issues are all source=gmc_api goes into the
	// GMC tab; everything else goes into the Crawler tab.
	var crawlerGroups, gmcGroups []issueGroup
	crawlerCount, gmcCount := 0, 0
	for _, g := range allGroups {
		var c, m []issueView
		for _, iss := range g.Issues {
			if iss.Source == "gmc_api" {
				m = append(m, iss)
			} else {
				c = append(c, iss)
			}
		}
		if len(c) > 0 {
			cg := g
			cg.Issues = c
			crawlerGroups = append(crawlerGroups, cg)
			crawlerCount += len(c)
		}
		if len(m) > 0 {
			mg := g
			mg.Issues = m
			gmcGroups = append(gmcGroups, mg)
			gmcCount += len(m)
		}
	}

	// 90-day timeline of account-status snapshots for this store. One row
	// per audit (or per background-refresh); the UI just lists them.
	var timeline []gmcTimelineEntry
	tlRows, terr := h.Pool.Query(ctx, `
		SELECT s.captured_at, COALESCE(s.account_status,''),
		       COALESCE(s.warnings_count,0), COALESCE(s.suspensions_count,0),
		       s.audit_id
		FROM gmc_account_snapshots s
		JOIN store_gmc_connections c ON c.id = s.gmc_connection_id
		WHERE c.tenant_id = $1 AND c.store_id = $2
		  AND s.captured_at > now() - interval '90 days'
		ORDER BY s.captured_at DESC
		LIMIT 50
	`, tenantID, storeID)
	if terr == nil {
		defer tlRows.Close()
		for tlRows.Next() {
			var e gmcTimelineEntry
			if err := tlRows.Scan(&e.CapturedAt, &e.AccountStatus,
				&e.WarningsCount, &e.SuspensionsCount, &e.AuditID); err != nil {
				continue
			}
			timeline = append(timeline, e)
		}
	}

	if !hasSnapshot && gmcCount == 0 && len(timeline) == 0 {
		return nil
	}

	// MerchantID lookup — a single query against the connection row.
	var merchantID string
	_ = h.Pool.QueryRow(ctx,
		`SELECT merchant_id FROM store_gmc_connections WHERE tenant_id=$1 AND store_id=$2 ORDER BY created_at DESC LIMIT 1`,
		tenantID, storeID,
	).Scan(&merchantID)

	return &auditGMCView{
		MerchantID:       merchantID,
		AccountStatus:    acctStatus,
		WebsiteClaimed:   websiteClaimed,
		WarningsCount:    warnings,
		SuspensionsCount: susp,
		ProductCount:     productCount,
		CrawlerGroups:    crawlerGroups,
		CrawlerCount:     crawlerCount,
		GMCGroups:        gmcGroups,
		GMCCount:         gmcCount,
		Timeline:         timeline,
	}
}

// AuditsList renders the per-tenant list of recent audits, with the
// trigger source and a per-audit diff summary (new/resolved counts +
// score delta) joined in.
func (h *Handlers) AuditsList(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	rows, err := h.Pool.Query(r.Context(), `
		SELECT a.id, a.store_id, s.shop_domain, a.status::text, a.score, a.risk_level,
		       a.started_at, a.finished_at, a.trigger,
		       d.new_issue_count, d.resolved_issue_count, d.score_delta,
		       d.previous_audit_id IS NOT NULL AS have_prev
		FROM audits a
		JOIN stores s ON s.id = a.store_id
		LEFT JOIN audit_diffs d ON d.audit_id = a.id
		WHERE a.tenant_id=$1
		ORDER BY a.created_at DESC
		LIMIT 100
	`, d.Tenant.ID)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not list audits.")
		return
	}
	defer rows.Close()
	type row struct {
		ID            uuid.UUID
		StoreID       uuid.UUID
		Domain        string
		Status        string
		Score         *int
		RiskLevel     *string
		StartedAt     *time.Time
		FinishedAt    *time.Time
		Trigger       string
		NewCount      int
		ResolvedCount int
		ScoreDelta    int
		HavePrev      bool
	}
	var list []row
	for rows.Next() {
		var rrow row
		var newCount, resolvedCount, scoreDelta *int
		if err := rows.Scan(&rrow.ID, &rrow.StoreID, &rrow.Domain, &rrow.Status,
			&rrow.Score, &rrow.RiskLevel, &rrow.StartedAt, &rrow.FinishedAt, &rrow.Trigger,
			&newCount, &resolvedCount, &scoreDelta, &rrow.HavePrev); err != nil {
			continue
		}
		if newCount != nil {
			rrow.NewCount = *newCount
		}
		if resolvedCount != nil {
			rrow.ResolvedCount = *resolvedCount
		}
		if scoreDelta != nil {
			rrow.ScoreDelta = *scoreDelta
		}
		list = append(list, rrow)
	}
	d.Title = "Audits"
	d.Data = list
	h.renderTenant(w, r, "audits-list", d)
}

// ----------------------------------------------------------------------------
// Issue resolve — flips fix_applied_at; informational only (does not change
// score). Idempotent.
// ----------------------------------------------------------------------------

func (h *Handlers) ResolveIssue(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	auditID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad audit id", http.StatusBadRequest)
		return
	}
	issueID, err := uuid.Parse(r.PathValue("issue_id"))
	if err != nil {
		http.Error(w, "bad issue id", http.StatusBadRequest)
		return
	}
	tag, err := h.Pool.Exec(r.Context(), `
		UPDATE issues
		SET fix_applied_at = COALESCE(fix_applied_at, now()),
		    fix_applied_by = COALESCE(fix_applied_by, $3),
		    updated_at     = now()
		WHERE tenant_id=$1 AND audit_id=$2 AND id=$4
	`, d.Tenant.ID, auditID, d.User.ID, issueID)
	if err != nil {
		h.Logger.Error("resolve issue", slog.Any("err", err))
		http.Error(w, "could not resolve", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "issue not found", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/t/%s/audits/%s#issue-%s", d.Tenant.Slug, auditID, issueID), http.StatusFound)
}

// ----------------------------------------------------------------------------
// PDF export — chromedp drives a headless browser through the print template
// and returns the rendered PDF.
// ----------------------------------------------------------------------------

// pdfChromePath is overridden by main if not running on a system with a
// stock chromium. Defaults to letting chromedp's exec allocator search PATH.
var pdfChromePath string

func (h *Handlers) ReportPDF(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	auditID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad audit id", http.StatusBadRequest)
		return
	}
	v, err := h.loadAudit(r.Context(), d.Tenant.ID, auditID)
	if err != nil || v.Status != "succeeded" {
		h.renderError(w, http.StatusNotFound, "Report not available.")
		return
	}
	groups, total, err := h.loadIssues(r.Context(), d.Tenant.ID, auditID)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not load issues.")
		return
	}
	v.IssueGroups = groups
	v.IssueCount = total

	// Render the print page to HTML, then hand it to chromedp via data: URL —
	// this avoids the "headless browser needs the user's session cookie"
	// problem entirely.
	t, err := h.Renderer.Page("audit-report-pdf")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cssBytes, _ := os.ReadFile("static/css/main.css")

	var htmlBuf bytes.Buffer
	if err := t.ExecuteTemplate(&htmlBuf, "layout-pdf", map[string]any{
		"Title":  "Audit report",
		"Tenant": d.Tenant, "Audit": v, "CSRFToken": d.CSRFToken,
		"GeneratedAt": time.Now().UTC().Format("Jan 2, 2006 15:04 UTC"),
		"InlineCSS":   template.CSS(cssBytes),
	}); err != nil {
		h.Logger.Error("render pdf html", slog.Any("err", err))
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	pdfBytes, err := renderPDF(r.Context(), htmlBuf.Bytes())
	if err != nil {
		h.Logger.Error("chromedp render", slog.Any("err", err))
		http.Error(w, "pdf render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="gmcauditor-%s-%s.pdf"`, v.StoreDomain, v.ID.String()[:8]))
	_, _ = w.Write(pdfBytes)
}

// renderPDF spins up a headless Chrome, loads the report HTML via a data: URL
// and returns the print-to-PDF bytes.
func renderPDF(ctx context.Context, html []byte) ([]byte, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("hide-scrollbars", true),
	)
	if pdfChromePath != "" {
		opts = append(opts, chromedp.ExecPath(pdfChromePath))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	tctx, cancelT := context.WithTimeout(browserCtx, 30*time.Second)
	defer cancelT()

	dataURL := "data:text/html;charset=utf-8;base64," + base64encode(html)
	var pdfBytes []byte
	err := chromedp.Run(tctx,
		chromedp.Navigate(dataURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, _, err := page.PrintToPDF().
				WithPrintBackground(true).
				WithPaperWidth(8.5).
				WithPaperHeight(11).
				WithMarginTop(0.4).
				WithMarginBottom(0.4).
				WithMarginLeft(0.4).
				WithMarginRight(0.4).
				Do(ctx)
			if err != nil {
				return err
			}
			pdfBytes = buf
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}
	if len(pdfBytes) == 0 {
		return nil, errors.New("empty pdf")
	}
	return pdfBytes, nil
}

// SetPDFChromePath lets main override the chromium binary path before
// requests start arriving (e.g. on dev machines that only have Playwright's
// bundled Chromium).
func SetPDFChromePath(p string) { pdfChromePath = p }

// base64encode wraps stdlib so the import block stays predictable.
var base64encode = func(b []byte) string {
	return stdBase64.StdEncoding.EncodeToString(b)
}
