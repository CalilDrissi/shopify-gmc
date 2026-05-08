package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlanResource enumerates the metered resources a plan limits.
type PlanResource string

const (
	ResStores         PlanResource = "stores"
	ResAuditsMonth    PlanResource = "audits_month"
	ResMembers        PlanResource = "members"
	ResGMCConnections PlanResource = "gmc_connections"
	ResMonitoring     PlanResource = "monitoring" // boolean: cadence != off
)

// EnforcePlanLimit returns nil when the tenant can perform the action, or
// a *PlanLimitError carrying a 402 body the handler should render. Caller
// passes (resource, "added") tuples; the gate measures current usage by
// SQL count so we don't have to keep counters in sync.
//
// For ResAuditsMonth we read usage_counters (monthly bucket); for the
// others we run a count(*) — cheap given the row counts these tables top
// out at on the largest plans.
func (h *Handlers) EnforcePlanLimit(ctx context.Context, tenantID uuid.UUID, plan string, res PlanResource) *PlanLimitError {
	q := QuotaFor(plan)
	limit := 0
	current := 0
	var msg string
	switch res {
	case ResStores:
		limit = q.Stores
		_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM stores WHERE tenant_id=$1`, tenantID).Scan(&current)
		msg = fmt.Sprintf("Your plan includes %d store%s; you've already added %d.", limit, plural(limit), current)
	case ResAuditsMonth:
		limit = q.AuditsPerMonth
		_ = h.Pool.QueryRow(ctx, `
			SELECT COALESCE(count, 0) FROM usage_counters
			WHERE tenant_id=$1 AND metric='audits' AND period_start = date_trunc('month', now())::date
		`, tenantID).Scan(&current)
		msg = fmt.Sprintf("Your plan includes %d audits per month; you've used %d.", limit, current)
	case ResMembers:
		limit = q.Members
		_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE tenant_id=$1`, tenantID).Scan(&current)
		msg = fmt.Sprintf("Your plan includes %d member%s; you already have %d.", limit, plural(limit), current)
	case ResGMCConnections:
		limit = q.GMCConnections
		_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM store_gmc_connections WHERE tenant_id=$1 AND status='active'`, tenantID).Scan(&current)
		msg = fmt.Sprintf("Your plan includes %d GMC connection%s; you already have %d.", limit, plural(limit), current)
	case ResMonitoring:
		// Monitoring is gated by cadence list, not a count — but the gate
		// helper still checks "is monitoring allowed at all". Caller (the
		// monitoring update handler) does the per-cadence check via
		// CadenceAllowed.
		if len(q.MonitoringCadences) <= 1 {
			return &PlanLimitError{
				Resource: res, Plan: plan, Limit: 0, Current: 0,
				Message: "Monitoring isn't included on this plan.",
			}
		}
		return nil
	}
	if current >= limit {
		return &PlanLimitError{Resource: res, Plan: plan, Limit: limit, Current: current, Message: msg}
	}
	return nil
}

// IncrementUsage bumps usage_counters.count for the current month bucket.
// Used by audit enqueue (ResAuditsMonth) so the limit query above sees the
// post-increment value.
func (h *Handlers) IncrementUsage(ctx context.Context, tenantID uuid.UUID, metric string) {
	_, err := h.Pool.Exec(ctx, `SELECT app_increment_usage($1, $2, 1)`, tenantID, metric)
	if err != nil {
		h.Logger.Warn("usage_increment_failed", "metric", metric, "tenant", tenantID, "err", err)
	}
}

// PlanLimitError carries the data the 402 page renders.
type PlanLimitError struct {
	Resource PlanResource
	Plan     string
	Limit    int
	Current  int
	Message  string
}

func (e *PlanLimitError) Error() string { return e.Message }

// RenderPlanLimit writes a 402 + the plan-limit page. Used by mutation
// handlers when EnforcePlanLimit returns non-nil.
func (h *Handlers) RenderPlanLimit(w http.ResponseWriter, r *http.Request, e *PlanLimitError) {
	d := h.buildTenantData(r)
	d.Title = "Upgrade to continue"
	d.Data = map[string]any{
		"Resource": string(e.Resource),
		"Plan":     e.Plan,
		"Limit":    e.Limit,
		"Current":  e.Current,
		"Message":  e.Message,
	}
	w.WriteHeader(http.StatusPaymentRequired)
	h.renderTenant(w, r, "plan-limit", d)
}

// MonthlyAuditsUsed returns this month's audit count for the tenant. Used
// by the dashboard usage card.
func (h *Handlers) MonthlyAuditsUsed(ctx context.Context, tenantID uuid.UUID) int {
	var n int
	_ = h.Pool.QueryRow(ctx, `
		SELECT COALESCE(count, 0) FROM usage_counters
		WHERE tenant_id=$1 AND metric='audits' AND period_start = date_trunc('month', now())::date
	`, tenantID).Scan(&n)
	return n
}

// plural returns "s" when n != 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// nextMonthStart is when the audit-month counter resets. Surfaced on the
// 402 page so users know how long until they can run another free audit.
func nextMonthStart() time.Time {
	t := time.Now().UTC()
	return time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

// strings used here so go imports it (even if linters can't tell).
var _ = strings.ToLower
