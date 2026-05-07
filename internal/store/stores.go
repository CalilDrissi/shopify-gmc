package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Shop struct {
	ID                     uuid.UUID
	TenantID               uuid.UUID
	ShopDomain             string
	DisplayName            *string
	AccessTokenEncrypted   []byte
	AccessTokenNonce       []byte
	Scope                  *string
	Status                 string
	MonitorEnabled         bool
	MonitorFrequency       time.Duration
	MonitorAlertThreshold  string
	LastAuditAt            *time.Time
	NextAuditAt            *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type StoresRepo struct{}

func (StoresRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, s *Shop) error {
	s.TenantID = tenantID
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO stores
		  (tenant_id, shop_domain, display_name, access_token_encrypted, access_token_nonce, scope)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, status::text, monitor_enabled, monitor_frequency,
		          monitor_alert_threshold::text, created_at, updated_at
	`, tenantID, s.ShopDomain, s.DisplayName, s.AccessTokenEncrypted, s.AccessTokenNonce, s.Scope,
	).Scan(&s.ID, &s.Status, &s.MonitorEnabled, &s.MonitorFrequency, &s.MonitorAlertThreshold, &s.CreatedAt, &s.UpdatedAt))
}

func (StoresRepo) GetByID(ctx context.Context, q Querier, tenantID, id uuid.UUID) (*Shop, error) {
	s := &Shop{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, shop_domain, display_name, access_token_encrypted, access_token_nonce,
		       scope, status::text, monitor_enabled, monitor_frequency, monitor_alert_threshold::text,
		       last_audit_at, next_audit_at, created_at, updated_at
		FROM stores
		WHERE tenant_id=$1 AND id=$2
	`, tenantID, id).Scan(&s.ID, &s.TenantID, &s.ShopDomain, &s.DisplayName, &s.AccessTokenEncrypted,
		&s.AccessTokenNonce, &s.Scope, &s.Status, &s.MonitorEnabled, &s.MonitorFrequency,
		&s.MonitorAlertThreshold, &s.LastAuditAt, &s.NextAuditAt, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return s, nil
}

func (StoresRepo) ListByTenant(ctx context.Context, q Querier, tenantID uuid.UUID) ([]Shop, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, shop_domain, display_name, access_token_encrypted, access_token_nonce,
		       scope, status::text, monitor_enabled, monitor_frequency, monitor_alert_threshold::text,
		       last_audit_at, next_audit_at, created_at, updated_at
		FROM stores
		WHERE tenant_id=$1
		ORDER BY created_at
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Shop
	for rows.Next() {
		var s Shop
		if err := rows.Scan(&s.ID, &s.TenantID, &s.ShopDomain, &s.DisplayName, &s.AccessTokenEncrypted,
			&s.AccessTokenNonce, &s.Scope, &s.Status, &s.MonitorEnabled, &s.MonitorFrequency,
			&s.MonitorAlertThreshold, &s.LastAuditAt, &s.NextAuditAt, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (StoresRepo) UpdateStatus(ctx context.Context, q Querier, tenantID, id uuid.UUID, status string) error {
	tag, err := q.Exec(ctx,
		`UPDATE stores SET status=$3::store_status, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
		tenantID, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (StoresRepo) UpdateMonitorSettings(ctx context.Context, q Querier, tenantID, id uuid.UUID, enabled bool, frequency time.Duration, threshold string) error {
	tag, err := q.Exec(ctx, `
		UPDATE stores
		SET monitor_enabled=$3, monitor_frequency=$4, monitor_alert_threshold=$5::issue_severity, updated_at=now()
		WHERE tenant_id=$1 AND id=$2
	`, tenantID, id, enabled, frequency, threshold)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
