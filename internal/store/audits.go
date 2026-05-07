package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Audit struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	StoreID      uuid.UUID
	TriggeredBy  *uuid.UUID
	Trigger      string
	Status       string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	ProductCount *int
	IssueCounts  []byte
	ErrorMessage *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AuditsRepo struct{}

func (AuditsRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, a *Audit) error {
	a.TenantID = tenantID
	if len(a.IssueCounts) == 0 {
		a.IssueCounts = []byte("{}")
	}
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO audits (tenant_id, store_id, triggered_by, trigger, issue_counts)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, status::text, created_at, updated_at
	`, tenantID, a.StoreID, a.TriggeredBy, a.Trigger, a.IssueCounts,
	).Scan(&a.ID, &a.Status, &a.CreatedAt, &a.UpdatedAt))
}

func (AuditsRepo) GetByID(ctx context.Context, q Querier, tenantID, id uuid.UUID) (*Audit, error) {
	a := &Audit{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, store_id, triggered_by, trigger, status::text,
		       started_at, finished_at, product_count, issue_counts, error_message,
		       created_at, updated_at
		FROM audits WHERE tenant_id=$1 AND id=$2
	`, tenantID, id).Scan(&a.ID, &a.TenantID, &a.StoreID, &a.TriggeredBy, &a.Trigger, &a.Status,
		&a.StartedAt, &a.FinishedAt, &a.ProductCount, &a.IssueCounts, &a.ErrorMessage,
		&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return a, nil
}

func (AuditsRepo) ListByStore(ctx context.Context, q Querier, tenantID, storeID uuid.UUID, limit int) ([]Audit, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, store_id, triggered_by, trigger, status::text,
		       started_at, finished_at, product_count, issue_counts, error_message,
		       created_at, updated_at
		FROM audits
		WHERE tenant_id=$1 AND store_id=$2
		ORDER BY created_at DESC
		LIMIT $3
	`, tenantID, storeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Audit
	for rows.Next() {
		var a Audit
		if err := rows.Scan(&a.ID, &a.TenantID, &a.StoreID, &a.TriggeredBy, &a.Trigger, &a.Status,
			&a.StartedAt, &a.FinishedAt, &a.ProductCount, &a.IssueCounts, &a.ErrorMessage,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (AuditsRepo) MarkRunning(ctx context.Context, q Querier, tenantID, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx, `
		UPDATE audits SET status='running', started_at=$3, updated_at=now()
		WHERE tenant_id=$1 AND id=$2 AND status='queued'
	`, tenantID, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (AuditsRepo) Finish(ctx context.Context, q Querier, tenantID, id uuid.UUID, status string,
	finishedAt time.Time, productCount int, issueCounts []byte, errMsg *string) error {
	if len(issueCounts) == 0 {
		issueCounts = []byte("{}")
	}
	tag, err := q.Exec(ctx, `
		UPDATE audits
		SET status=$3::audit_status, finished_at=$4, product_count=$5, issue_counts=$6,
		    error_message=$7, updated_at=now()
		WHERE tenant_id=$1 AND id=$2
	`, tenantID, id, status, finishedAt, productCount, issueCounts, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
