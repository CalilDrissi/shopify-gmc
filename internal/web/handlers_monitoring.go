package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/audit/differ"
)

// monitoringView is what the store-detail template needs to render the
// Monitoring panel + trendline + alert subscriptions.
type monitoringView struct {
	Cadence            string   // "off" | "daily" | "weekly" | "monthly"
	NextRunAt          *time.Time
	LastRunAt          *time.Time
	AvailableCadences  []string // gated by plan
	AllCadences        []string // for rendering disabled options
	PlanName           string

	Trendline   trendline
	Subscription subscriptionView
	LastDiff    *diffSummary
}

type subscriptionView struct {
	ID                 uuid.UUID
	Exists             bool
	OnNewCritical      bool
	OnScoreDrop        bool
	OnAuditFailed      bool
	OnGMCChange        bool
	ScoreDropThreshold int
}

type diffSummary struct {
	NewCount         int
	ResolvedCount    int
	NewCriticalCount int
	ScoreDelta       int
	HavePrev         bool
	NewIssues        []differ.IssueKey
}

// trendline holds the rendered SVG for the last-12-scores chart so the
// template doesn't have to build it from raw data points.
type trendline struct {
	Points []trendPoint
	SVG    template.HTML // pre-rendered SVG
	Empty  bool
}

type trendPoint struct {
	Score    int
	WhenText string
}

func (h *Handlers) loadMonitoring(r *http.Request, tenantID, storeID uuid.UUID, plan string,
	monitorEnabled bool, monitorFrequency time.Duration,
	nextRunAt, lastRunAt *time.Time, userID uuid.UUID) *monitoringView {

	cadence := "off"
	if monitorEnabled {
		switch {
		case monitorFrequency <= 25*time.Hour:
			cadence = "daily"
		case monitorFrequency <= 8*24*time.Hour:
			cadence = "weekly"
		case monitorFrequency <= 32*24*time.Hour:
			cadence = "monthly"
		default:
			cadence = "weekly"
		}
	}

	view := &monitoringView{
		Cadence:           cadence,
		NextRunAt:         nextRunAt,
		LastRunAt:         lastRunAt,
		AvailableCadences: MonitoringCadences(plan),
		AllCadences:       []string{"off", "daily", "weekly", "monthly"},
		PlanName:          plan,
	}

	// Trendline: last 12 succeeded audits, oldest → newest.
	rows, err := h.Pool.Query(r.Context(), `
		SELECT score, COALESCE(finished_at, created_at)
		FROM audits
		WHERE tenant_id = $1 AND store_id = $2 AND status = 'succeeded' AND score IS NOT NULL
		ORDER BY COALESCE(finished_at, created_at) DESC
		LIMIT 12
	`, tenantID, storeID)
	if err == nil {
		var pts []trendPoint
		for rows.Next() {
			var p trendPoint
			var when time.Time
			if err := rows.Scan(&p.Score, &when); err != nil {
				continue
			}
			p.WhenText = when.Format("Jan 2 15:04")
			pts = append(pts, p)
		}
		rows.Close()
		// Reverse to chronological.
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
		view.Trendline.Points = pts
		view.Trendline.SVG = template.HTML(renderTrendlineSVG(pts))
		view.Trendline.Empty = len(pts) == 0
	}

	// Current user's subscription for this store.
	var sub subscriptionView
	err = h.Pool.QueryRow(r.Context(), `
		SELECT id, on_new_critical, on_score_drop, on_audit_failed, on_gmc_account_change, score_drop_threshold
		FROM store_alert_subscriptions
		WHERE tenant_id = $1 AND store_id = $2 AND user_id = $3
	`, tenantID, storeID, userID).Scan(
		&sub.ID, &sub.OnNewCritical, &sub.OnScoreDrop, &sub.OnAuditFailed, &sub.OnGMCChange, &sub.ScoreDropThreshold,
	)
	if err == nil {
		sub.Exists = true
	} else {
		// Defaults that match the schema.
		sub.OnNewCritical = true
		sub.OnScoreDrop = true
		sub.OnAuditFailed = true
		sub.ScoreDropThreshold = 10
	}
	view.Subscription = sub

	// Last audit's diff summary (if any), for the audit detail page; the
	// store detail page uses this for the "since last run" hint.
	var (
		newCount, resolvedCount, newCritical, scoreDelta int
		havePrev                                          bool
		diffJSON                                          []byte
	)
	err = h.Pool.QueryRow(r.Context(), `
		SELECT d.new_issue_count, d.resolved_issue_count, d.new_critical_count,
		       COALESCE(d.score_delta, 0),
		       d.previous_audit_id IS NOT NULL,
		       d.diff
		FROM audit_diffs d
		JOIN audits a ON a.id = d.audit_id
		WHERE a.tenant_id = $1 AND a.store_id = $2 AND a.status = 'succeeded'
		ORDER BY a.finished_at DESC
		LIMIT 1
	`, tenantID, storeID).Scan(&newCount, &resolvedCount, &newCritical, &scoreDelta, &havePrev, &diffJSON)
	if err == nil && havePrev {
		var d differ.Diff
		_ = json.Unmarshal(diffJSON, &d)
		view.LastDiff = &diffSummary{
			NewCount:         newCount,
			ResolvedCount:    resolvedCount,
			NewCriticalCount: newCritical,
			ScoreDelta:       scoreDelta,
			HavePrev:         havePrev,
			NewIssues:        d.NewIssues,
		}
	}

	return view
}

// renderTrendlineSVG draws a 280×80 SVG line chart of the score points.
// Pure stdlib — no JS, no external libs. Score range is fixed at 0..100.
func renderTrendlineSVG(pts []trendPoint) string {
	const w, h = 280, 80
	const padX, padY = 8, 8
	if len(pts) == 0 {
		return fmt.Sprintf(`<svg viewBox="0 0 %d %d" class="c-trend"><text x="%d" y="%d" fill="var(--md-sys-color-on-surface-variant)" font-size="11">No score history yet</text></svg>`,
			w, h, padX, h/2)
	}
	innerW := float64(w - 2*padX)
	innerH := float64(h - 2*padY)
	step := innerW
	if len(pts) > 1 {
		step = innerW / float64(len(pts)-1)
	}
	var path, dots, gridLabel string
	for i, p := range pts {
		x := float64(padX) + float64(i)*step
		y := float64(padY) + (1-float64(p.Score)/100)*innerH
		if i == 0 {
			path += fmt.Sprintf("M %.1f %.1f", x, y)
		} else {
			path += fmt.Sprintf(" L %.1f %.1f", x, y)
		}
		color := scoreColor(p.Score)
		dots += fmt.Sprintf(`<circle cx="%.1f" cy="%.1f" r="3" fill="%s"><title>%d on %s</title></circle>`, x, y, color, p.Score, p.WhenText)
	}
	last := pts[len(pts)-1]
	gridLabel = fmt.Sprintf(`<text x="%d" y="%d" fill="var(--md-sys-color-on-surface-variant)" font-size="10">latest %d</text>`,
		w-50, padY+10, last.Score)
	stroke := scoreColor(last.Score)
	return fmt.Sprintf(`<svg viewBox="0 0 %d %d" class="c-trend" role="img" aria-label="last 12 audit scores">
<line x1="%d" x2="%d" y1="%d" y2="%d" stroke="var(--md-sys-color-outline-variant)" stroke-dasharray="2 3"/>
<line x1="%d" x2="%d" y1="%d" y2="%d" stroke="var(--md-sys-color-outline-variant)" stroke-dasharray="2 3"/>
<path d="%s" fill="none" stroke="%s" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
%s
%s
</svg>`,
		w, h,
		padX, w-padX, padY+int(innerH*0.2), padY+int(innerH*0.2),
		padX, w-padX, padY+int(innerH*0.5), padY+int(innerH*0.5),
		path, stroke, dots, gridLabel)
}

func scoreColor(s int) string {
	switch {
	case s >= 80:
		return "#1b873f"
	case s >= 50:
		return "#b76b00"
	default:
		return "#b3261e"
	}
}

// ----------------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------------

// MonitoringUpdate sets monitor_enabled + monitor_frequency for the store.
// Plan-gated: cadence must be in MonitoringCadences(tenant.Plan).
func (h *Handlers) MonitoringUpdate(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	storeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad store id", http.StatusBadRequest)
		return
	}
	cadence := r.FormValue("cadence")
	// Enabling monitoring at all is gated; the per-cadence allowlist is the
	// finer-grained check that follows.
	if cadence != "off" {
		if e := h.EnforcePlanLimit(r.Context(), d.Tenant.ID, string(d.Tenant.Plan), ResMonitoring); e != nil {
			h.RenderPlanLimit(w, r, e)
			return
		}
	}
	if !CadenceAllowed(string(d.Tenant.Plan), cadence) {
		h.RenderPlanLimit(w, r, &PlanLimitError{
			Resource: ResMonitoring, Plan: string(d.Tenant.Plan), Limit: 0, Current: 0,
			Message: fmt.Sprintf("The %q cadence isn't included on your plan.", cadence),
		})
		return
	}
	enabled := cadence != "off"
	interval := CadenceInterval(cadence)

	if enabled {
		// Set monitor_frequency + reset next_audit_at so the scheduler picks
		// it up promptly (won't wait an entire interval after enabling).
		_, err = h.Pool.Exec(r.Context(), `
			UPDATE stores
			SET monitor_enabled    = true,
			    monitor_frequency  = $3::interval,
			    next_audit_at      = COALESCE(next_audit_at, now()),
			    updated_at         = now()
			WHERE tenant_id = $1 AND id = $2
		`, d.Tenant.ID, storeID, interval)
	} else {
		_, err = h.Pool.Exec(r.Context(), `
			UPDATE stores
			SET monitor_enabled = false, updated_at = now()
			WHERE tenant_id = $1 AND id = $2
		`, d.Tenant.ID, storeID)
	}
	if err != nil {
		h.Logger.Error("monitoring update", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not update monitoring.")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/t/%s/stores/%s", d.Tenant.Slug, storeID), http.StatusFound)
}

// RunNow forces a scheduled-style audit immediately. Same atomic pattern as
// the scheduler's claim+enqueue but driven by a button click.
func (h *Handlers) RunNow(w http.ResponseWriter, r *http.Request) {
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

	auditID := uuid.New()
	storeURL := StoreURLFor(s.ShopDomain)
	storeName := s.ShopDomain
	if s.DisplayName != nil && *s.DisplayName != "" {
		storeName = *s.DisplayName
	}

	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not start audit.")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO audits (id, tenant_id, store_id, triggered_by, trigger, status, started_at, issue_counts, progress)
		VALUES ($1, $2, $3, NULL, 'scheduled', 'queued', NULL, '{}'::jsonb, '[]'::jsonb)
	`, auditID, d.Tenant.ID, s.ID); err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not start audit.")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"audit_id":   auditID,
		"tenant_id":  d.Tenant.ID,
		"store_id":   s.ID,
		"store_url":  storeURL,
		"store_name": storeName,
		"trigger":    "scheduled",
	})
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO audit_jobs (tenant_id, kind, payload, run_at)
		VALUES ($1, 'audit_store', $2, now())
	`, d.Tenant.ID, payload); err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not enqueue audit.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not save audit.")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/t/%s/audits/%s", d.Tenant.Slug, auditID), http.StatusFound)
}

// SubscriptionUpdate upserts the current user's alert subscription for
// this store. POST checkboxes on/off; missing checkbox = off (HTML form
// quirk). Form-only: no JS dependency.
func (h *Handlers) SubscriptionUpdate(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	storeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad store id", http.StatusBadRequest)
		return
	}
	flag := func(key string) bool { return r.FormValue(key) == "on" }
	threshold, _ := strconv.Atoi(r.FormValue("score_drop_threshold"))
	if threshold < 1 {
		threshold = 10
	}
	_, err = h.Pool.Exec(r.Context(), `
		INSERT INTO store_alert_subscriptions
			(tenant_id, store_id, user_id, channel, target, min_severity, enabled,
			 on_new_critical, on_score_drop, on_audit_failed, on_gmc_account_change, score_drop_threshold)
		VALUES
			($1, $2, $3, 'email', $4, 'warning', true,
			 $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, store_id, user_id, channel) WHERE store_id IS NOT NULL AND user_id IS NOT NULL DO UPDATE SET
			on_new_critical       = EXCLUDED.on_new_critical,
			on_score_drop         = EXCLUDED.on_score_drop,
			on_audit_failed       = EXCLUDED.on_audit_failed,
			on_gmc_account_change = EXCLUDED.on_gmc_account_change,
			score_drop_threshold  = EXCLUDED.score_drop_threshold,
			enabled               = true,
			updated_at            = now()
	`, d.Tenant.ID, storeID, d.User.ID, d.User.Email,
		flag("on_new_critical"), flag("on_score_drop"), flag("on_audit_failed"), flag("on_gmc_account_change"),
		threshold)
	if err != nil {
		h.Logger.Error("subscription update", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not save alert preferences.")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/t/%s/stores/%s", d.Tenant.Slug, storeID), http.StatusFound)
}
