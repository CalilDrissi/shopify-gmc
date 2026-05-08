// Package scheduler claims due-for-monitoring stores and enqueues audit jobs.
//
// One scheduler instance per cluster is fine — the claim query uses
// FOR UPDATE SKIP LOCKED so multiple schedulers running concurrently never
// double-enqueue. We default to a 60 second tick and a per-tick batch of
// 100 stores.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StoreURLBuilder turns a stored shop_domain into the URL the audit
// pipeline should crawl. The web layer uses the same helper for manual
// audits, so callers pass it in instead of duplicating the rule here.
type StoreURLBuilder func(shopDomain string) string

type Scheduler struct {
	Pool      *pgxpool.Pool
	Logger    *slog.Logger
	Tick      time.Duration   // default 60s
	BatchSize int             // default 100
	URLFor    StoreURLBuilder // required
}

const defaultTick = 60 * time.Second
const defaultBatch = 100

// Run blocks until ctx is cancelled. Every tick: claim due stores, advance
// next_audit_at, create an audits row + audit_jobs row for each.
func (s *Scheduler) Run(ctx context.Context) error {
	if s.Tick == 0 {
		s.Tick = defaultTick
	}
	if s.BatchSize == 0 {
		s.BatchSize = defaultBatch
	}
	if s.URLFor == nil {
		return fmt.Errorf("scheduler: URLFor is required")
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	s.Logger.Info("scheduler_start",
		slog.Duration("tick", s.Tick),
		slog.Int("batch_size", s.BatchSize),
	)

	t := time.NewTicker(s.Tick)
	defer t.Stop()

	// Run once immediately so we don't have to wait a full tick on boot.
	s.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			s.runOnce(ctx)
		}
	}
}

type claimedStore struct {
	StoreID     uuid.UUID
	TenantID    uuid.UUID
	ShopDomain  string
	DisplayName *string
	NextRunAt   time.Time // the *new* next_audit_at after this tick
}

// runOnce claims due stores and enqueues an audit for each. Logged at INFO
// when work was done; errors are logged at WARN and the loop keeps running.
func (s *Scheduler) runOnce(ctx context.Context) {
	t0 := time.Now()
	claimed, err := s.claimDue(ctx)
	if err != nil {
		s.Logger.Warn("scheduler_claim_err", slog.Any("err", err))
		return
	}
	if len(claimed) == 0 {
		s.Logger.Debug("scheduler_idle")
		return
	}
	enq, fail := 0, 0
	for _, c := range claimed {
		if err := s.enqueueAudit(ctx, c); err != nil {
			s.Logger.Warn("scheduler_enqueue_err",
				slog.String("store_id", c.StoreID.String()),
				slog.Any("err", err))
			fail++
			continue
		}
		enq++
	}
	s.Logger.Info("scheduler_tick",
		slog.Int("claimed", len(claimed)),
		slog.Int("enqueued", enq),
		slog.Int("failed", fail),
		slog.Duration("dur", time.Since(t0)),
	)
}

// claimDue atomically locks up to BatchSize stores whose next_audit_at has
// elapsed, advances next_audit_at = now() + monitor_frequency, sets
// last_audit_at = now(), and returns the rows so we can enqueue jobs.
//
// The CTE pattern + SKIP LOCKED + same-tx UPDATE means concurrent schedulers
// never claim the same row twice — the row gets a row-lock that survives
// until commit, after which next_audit_at is in the future.
func (s *Scheduler) claimDue(ctx context.Context) ([]claimedStore, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH due AS (
			SELECT id, monitor_frequency
			FROM stores
			WHERE monitor_enabled
			  AND (next_audit_at IS NULL OR next_audit_at <= now())
			ORDER BY next_audit_at NULLS FIRST
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE stores s
		SET next_audit_at = now() + due.monitor_frequency,
		    last_audit_at = now(),
		    updated_at    = now()
		FROM due
		WHERE s.id = due.id
		RETURNING s.id, s.tenant_id, s.shop_domain, s.display_name, s.next_audit_at
	`, s.BatchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []claimedStore
	for rows.Next() {
		var c claimedStore
		if err := rows.Scan(&c.StoreID, &c.TenantID, &c.ShopDomain, &c.DisplayName, &c.NextRunAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// enqueueAudit inserts an audits row at status='queued' with triggered_by
// NULL (scheduled) and an audit_jobs row that the worker will pick up.
// One transaction per store keeps a flapping store from holding back others.
func (s *Scheduler) enqueueAudit(ctx context.Context, c claimedStore) error {
	auditID := uuid.New()
	storeName := c.ShopDomain
	if c.DisplayName != nil && *c.DisplayName != "" {
		storeName = *c.DisplayName
	}
	storeURL := s.URLFor(c.ShopDomain)

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO audits (id, tenant_id, store_id, triggered_by, trigger, status, started_at, issue_counts, progress)
		VALUES ($1, $2, $3, NULL, 'scheduled', 'queued', NULL, '{}'::jsonb, '[]'::jsonb)
	`, auditID, c.TenantID, c.StoreID); err != nil {
		return fmt.Errorf("insert audit: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"audit_id":   auditID,
		"tenant_id":  c.TenantID,
		"store_id":   c.StoreID,
		"store_url":  storeURL,
		"store_name": storeName,
		"trigger":    "scheduled",
	})

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_jobs (tenant_id, kind, payload, run_at)
		VALUES ($1, 'audit_store', $2, now())
	`, c.TenantID, payload); err != nil {
		return fmt.Errorf("insert job: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.Logger.Info("scheduler_enqueued",
		slog.String("store_id", c.StoreID.String()),
		slog.String("shop_domain", c.ShopDomain),
		slog.String("audit_id", auditID.String()),
		slog.Time("next_run_at", c.NextRunAt),
	)
	return nil
}

// Force exposes the same enqueue path for the "Run now" button (UI), so the
// scheduler and the manual button share atomicity guarantees.
func (s *Scheduler) Force(ctx context.Context, tx pgx.Tx, tenantID, storeID uuid.UUID, shopDomain, displayName string) (uuid.UUID, error) {
	auditID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO audits (id, tenant_id, store_id, triggered_by, trigger, status, started_at, issue_counts, progress)
		VALUES ($1, $2, $3, NULL, 'scheduled', 'queued', NULL, '{}'::jsonb, '[]'::jsonb)
	`, auditID, tenantID, storeID); err != nil {
		return uuid.Nil, err
	}
	payload, _ := json.Marshal(map[string]any{
		"audit_id":   auditID,
		"tenant_id":  tenantID,
		"store_id":   storeID,
		"store_url":  s.URLFor(shopDomain),
		"store_name": coalesceStr(displayName, shopDomain),
		"trigger":    "scheduled",
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_jobs (tenant_id, kind, payload, run_at)
		VALUES ($1, 'audit_store', $2, now())
	`, tenantID, payload); err != nil {
		return uuid.Nil, err
	}
	return auditID, nil
}

func coalesceStr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}
