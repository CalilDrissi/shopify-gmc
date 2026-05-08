package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/gmc"
)

// GMCRefresher periodically pings every active store_gmc_connections row
// to keep the cached account_status / warnings_count / last_sync_at flags
// fresh — independent of audit runs. We tick every minute internally
// and only call Google for connections whose plan-derived cadence has
// elapsed since their last_sync_at.
//
// On 401 from Google we mark the connection revoked + email the owner
// (delegated to the supplied OnUnauthorized callback so the worker stays
// the only place that knows how to compose that mail).
//
// On 429 we just log — gmc.Client already does exponential backoff up to
// 1h before returning ErrRateLimited.
type GMCRefresher struct {
	Pool          *pgxpool.Pool
	Conns         *gmc.ConnectionStore
	GMCBaseURL    string
	Logger        *slog.Logger
	Tick          time.Duration // default 60s
	OnUnauthorized func(ctx context.Context, conn *gmc.Connection)
}

func (r *GMCRefresher) Run(ctx context.Context) {
	if r.Conns == nil {
		r.Logger.Info("gmc_refresher_disabled", slog.String("reason", "no GMC connection store"))
		return
	}
	if r.Tick == 0 {
		r.Tick = time.Minute
	}
	if r.Logger == nil {
		r.Logger = slog.Default()
	}
	r.Logger.Info("gmc_refresher_start", slog.Duration("tick", r.Tick))

	t := time.NewTicker(r.Tick)
	defer t.Stop()
	r.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.runOnce(ctx)
		}
	}
}

type dueConnection struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	StoreID    uuid.UUID
	MerchantID string
	Plan       string
}

func (r *GMCRefresher) runOnce(ctx context.Context) {
	due, err := r.loadDue(ctx)
	if err != nil {
		r.Logger.Warn("gmc_refresh_query", slog.Any("err", err))
		return
	}
	if len(due) == 0 {
		return
	}
	for _, c := range due {
		r.refreshOne(ctx, c)
	}
}

// loadDue returns connections whose plan-cadence interval has elapsed
// since the last successful sync. Free/Starter cadences (no background
// refresh) are excluded by the WHERE clause.
//
// Cadence is derived from the tenant's plan inside Go because Postgres
// doesn't know our plan→cadence mapping; we filter rows that have any
// plausible cadence elapsed and let the Go side make the final call.
func (r *GMCRefresher) loadDue(ctx context.Context) ([]dueConnection, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT c.id, c.tenant_id, c.store_id, c.merchant_id, t.plan::text
		FROM store_gmc_connections c
		JOIN tenants t ON t.id = c.tenant_id
		WHERE c.status = 'active'
		  AND (c.last_sync_at IS NULL OR c.last_sync_at < now() - interval '5 minutes')
		ORDER BY c.last_sync_at NULLS FIRST
		LIMIT 200
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dueConnection
	for rows.Next() {
		var d dueConnection
		if err := rows.Scan(&d.ID, &d.TenantID, &d.StoreID, &d.MerchantID, &d.Plan); err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// cadenceFor mirrors web.GMCFor — duplicated here to avoid an import
// cycle (web → scheduler → web). Keep in sync.
func cadenceFor(plan string) time.Duration {
	switch strings.ToLower(plan) {
	case "growth":
		return time.Hour
	case "agency":
		return 15 * time.Minute
	case "enterprise":
		return 5 * time.Minute
	}
	return 0 // free / starter — no background refresh
}

func (r *GMCRefresher) refreshOne(ctx context.Context, c dueConnection) {
	cadence := cadenceFor(c.Plan)
	if cadence == 0 {
		return
	}
	// Re-check the cadence window with a per-row read (loadDue used a
	// loose 5-minute floor). If the row was synced more recently than
	// `cadence` ago, skip.
	var lastSync *time.Time
	if err := r.Pool.QueryRow(ctx,
		`SELECT last_sync_at FROM store_gmc_connections WHERE id = $1`, c.ID,
	).Scan(&lastSync); err != nil {
		return
	}
	if lastSync != nil && time.Since(*lastSync) < cadence {
		return
	}

	conn, err := r.Conns.GetByStore(ctx, c.TenantID, c.StoreID)
	if err != nil {
		return
	}

	cli := gmc.NewClient(r.Conns.SupplierFor(conn.ID), r.Logger)
	if r.GMCBaseURL != "" {
		cli.BaseURL = r.GMCBaseURL
	}
	acct, err := cli.GetAccountStatus(ctx, conn.MerchantID)
	if err != nil {
		if errors.Is(err, gmc.ErrUnauthorized) {
			r.Logger.Warn("gmc_refresh_401", slog.String("connection_id", conn.ID.String()))
			_ = r.Conns.MarkRevoked(ctx, conn.ID, "Google rejected our refresh token (401) during background refresh.")
			if r.OnUnauthorized != nil {
				r.OnUnauthorized(ctx, conn)
			}
			return
		}
		if errors.Is(err, gmc.ErrRateLimited) {
			r.Logger.Warn("gmc_refresh_429", slog.String("connection_id", conn.ID.String()))
		} else {
			r.Logger.Warn("gmc_refresh_err", slog.String("connection_id", conn.ID.String()), slog.Any("err", err))
		}
		_ = r.Conns.Touch(ctx, conn.ID, "error", err.Error())
		return
	}
	_ = r.Conns.Touch(ctx, conn.ID, "ok", "")
	_ = r.Conns.UpdateAccountSummary(ctx, conn.ID, acct)
	r.Logger.Info("gmc_refresh_done",
		slog.String("connection_id", conn.ID.String()),
		slog.String("merchant_id", conn.MerchantID),
		slog.String("status", acct.Status),
	)
}
