package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type UsageCounter struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	PeriodStart time.Time
	PeriodEnd   time.Time
	Metric      string
	Count       int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type UsageCountersRepo struct{}

func (UsageCountersRepo) Increment(ctx context.Context, q Querier, tenantID uuid.UUID, metric string, periodStart, periodEnd time.Time, delta int64) (*UsageCounter, error) {
	c := &UsageCounter{}
	err := q.QueryRow(ctx, `
		INSERT INTO usage_counters (tenant_id, period_start, period_end, metric, count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, period_start, metric) DO UPDATE
			SET count = usage_counters.count + EXCLUDED.count,
			    updated_at = now()
		RETURNING id, tenant_id, period_start, period_end, metric, count, created_at, updated_at
	`, tenantID, periodStart, periodEnd, metric, delta).Scan(
		&c.ID, &c.TenantID, &c.PeriodStart, &c.PeriodEnd, &c.Metric, &c.Count, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return c, nil
}

func (UsageCountersRepo) Get(ctx context.Context, q Querier, tenantID uuid.UUID, metric string, periodStart time.Time) (*UsageCounter, error) {
	c := &UsageCounter{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, period_start, period_end, metric, count, created_at, updated_at
		FROM usage_counters
		WHERE tenant_id=$1 AND metric=$2 AND period_start=$3
	`, tenantID, metric, periodStart).Scan(
		&c.ID, &c.TenantID, &c.PeriodStart, &c.PeriodEnd, &c.Metric, &c.Count, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return c, nil
}
