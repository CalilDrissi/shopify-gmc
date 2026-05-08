package web

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlanLimits maps tenant plan strings to their store quotas. The schema's
// plan_tier enum currently has free/pro/agency/enterprise; this layer also
// recognises the canonical "starter"/"growth" names so we can swap the enum
// later without touching the limit logic.
var PlanLimits = map[string]int{
	"free":       1,
	"starter":    3,
	"pro":        3, // existing enum value, treated as "starter"
	"growth":     10,
	"agency":     50,
	"enterprise": 500,
}

// LimitFor returns the store quota for a plan, defaulting to the Free tier.
func LimitFor(plan string) int {
	if n, ok := PlanLimits[strings.ToLower(plan)]; ok {
		return n
	}
	return PlanLimits["free"]
}

// PlanQuota is the full per-plan quota table. Spec lookup keyed by canonical
// plan name; the schema's `pro` is treated as `starter`.
//
// Keep PlanLimits (the legacy single-quota map) in sync with Stores below.
type PlanQuota struct {
	Stores             int
	AuditsPerMonth     int
	Members            int
	GMCConnections     int
	MonitoringCadences []string // matches MonitoringCadences()
	WhiteLabel         bool
}

func QuotaFor(plan string) PlanQuota {
	switch strings.ToLower(plan) {
	case "free":
		return PlanQuota{Stores: 1, AuditsPerMonth: 1, Members: 3, GMCConnections: 0, MonitoringCadences: []string{"off"}}
	case "starter", "pro":
		return PlanQuota{Stores: 3, AuditsPerMonth: 10, Members: 5, GMCConnections: 1, MonitoringCadences: []string{"off", "weekly"}}
	case "growth":
		return PlanQuota{Stores: 10, AuditsPerMonth: 50, Members: 15, GMCConnections: 3, MonitoringCadences: []string{"off", "daily", "weekly", "monthly"}}
	case "agency":
		return PlanQuota{Stores: 50, AuditsPerMonth: 500, Members: 50, GMCConnections: 50, MonitoringCadences: []string{"off", "daily", "weekly", "monthly"}, WhiteLabel: true}
	case "enterprise":
		return PlanQuota{Stores: 500, AuditsPerMonth: 5000, Members: 500, GMCConnections: 500, MonitoringCadences: []string{"off", "daily", "weekly", "monthly"}, WhiteLabel: true}
	}
	return PlanQuota{Stores: 1, AuditsPerMonth: 1, Members: 3, GMCConnections: 0, MonitoringCadences: []string{"off"}}
}

// CheckStoreLimit returns (limit, current, atLimit). It uses the connection
// pool directly (no SET LOCAL needed because we're filtering by tenant_id
// explicitly).
func CheckStoreLimit(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, plan string) (limit int, current int, atLimit bool, err error) {
	limit = LimitFor(plan)
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM stores WHERE tenant_id=$1`, tenantID,
	).Scan(&current); err != nil {
		return limit, 0, false, err
	}
	return limit, current, current >= limit, nil
}

// MonitoringCadences enumerates the monitoring cadences a plan grants.
// Spec: Free no monitoring, Starter (=pro) weekly only, Growth+ all cadences.
//
// Returned slice is in ascending-frequency order: "off" is always first so
// the UI can render "Disable monitoring" as the safe default. Cadences not
// in the slice render as disabled options with an upgrade tooltip.
func MonitoringCadences(plan string) []string {
	switch strings.ToLower(plan) {
	case "free":
		return []string{"off"}
	case "starter", "pro":
		return []string{"off", "weekly"}
	case "growth", "agency", "enterprise":
		return []string{"off", "daily", "weekly", "monthly"}
	default:
		return []string{"off"}
	}
}

// CadenceAllowed reports whether the requested cadence is permitted for
// this tenant's plan. Used to guard the monitoring-update handler.
func CadenceAllowed(plan, cadence string) bool {
	for _, c := range MonitoringCadences(plan) {
		if c == cadence {
			return true
		}
	}
	return false
}

// CadenceInterval converts a cadence label to a Postgres interval string
// suitable for the stores.monitor_frequency column.
func CadenceInterval(cadence string) string {
	switch cadence {
	case "daily":
		return "1 day"
	case "weekly":
		return "7 days"
	case "monthly":
		return "30 days"
	}
	return ""
}

// ----------------------------------------------------------------------------
// GMC plan gating
// ----------------------------------------------------------------------------

// GMCPlan describes what a tenant's plan grants them on the GMC side.
//
// Spec mapping:
//   - Free     → no GMC at all
//   - Starter  → 1 manual sync (sync only when triggered by the user / audit)
//   - Growth   → 3 connections, hourly background refresh
//   - Agency   → 50 connections, 15-minute priority refresh
type GMCPlan struct {
	Allowed             bool
	MaxConnections      int
	BackgroundEvery     time.Duration // 0 = manual only
	Label               string
}

// GMCFor returns the gating for a plan. Lookups are case-insensitive.
func GMCFor(plan string) GMCPlan {
	switch strings.ToLower(plan) {
	case "free":
		return GMCPlan{Allowed: false, Label: "Free"}
	case "starter", "pro":
		return GMCPlan{Allowed: true, MaxConnections: 1, BackgroundEvery: 0, Label: "Starter"}
	case "growth":
		return GMCPlan{Allowed: true, MaxConnections: 3, BackgroundEvery: time.Hour, Label: "Growth"}
	case "agency":
		return GMCPlan{Allowed: true, MaxConnections: 50, BackgroundEvery: 15 * time.Minute, Label: "Agency"}
	case "enterprise":
		return GMCPlan{Allowed: true, MaxConnections: 500, BackgroundEvery: 5 * time.Minute, Label: "Enterprise"}
	}
	return GMCPlan{Allowed: false, Label: plan}
}
