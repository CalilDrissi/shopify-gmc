package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type StoreAlertSubscription struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	StoreID     *uuid.UUID
	UserID      *uuid.UUID
	Channel     string
	Target      string
	MinSeverity string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type StoreAlertSubscriptionsRepo struct{}

func (StoreAlertSubscriptionsRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, s *StoreAlertSubscription) error {
	s.TenantID = tenantID
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO store_alert_subscriptions
		  (tenant_id, store_id, user_id, channel, target, min_severity)
		VALUES ($1, $2, $3, $4::alert_channel, $5, COALESCE(NULLIF($6,'')::issue_severity, 'warning'))
		RETURNING id, channel::text, min_severity::text, enabled, created_at, updated_at
	`, tenantID, s.StoreID, s.UserID, s.Channel, s.Target, s.MinSeverity,
	).Scan(&s.ID, &s.Channel, &s.MinSeverity, &s.Enabled, &s.CreatedAt, &s.UpdatedAt))
}

func (StoreAlertSubscriptionsRepo) ListByTenant(ctx context.Context, q Querier, tenantID uuid.UUID) ([]StoreAlertSubscription, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, store_id, user_id, channel::text, target,
		       min_severity::text, enabled, created_at, updated_at
		FROM store_alert_subscriptions
		WHERE tenant_id=$1
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoreAlertSubscription
	for rows.Next() {
		var s StoreAlertSubscription
		if err := rows.Scan(&s.ID, &s.TenantID, &s.StoreID, &s.UserID, &s.Channel, &s.Target,
			&s.MinSeverity, &s.Enabled, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (StoreAlertSubscriptionsRepo) SetEnabled(ctx context.Context, q Querier, tenantID, id uuid.UUID, enabled bool) error {
	tag, err := q.Exec(ctx,
		`UPDATE store_alert_subscriptions SET enabled=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
		tenantID, id, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
