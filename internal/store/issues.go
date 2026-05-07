package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Issue struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	AuditID         uuid.UUID
	StoreID         uuid.UUID
	ProductID       *string
	ProductTitle    *string
	RuleCode        string
	Severity        string
	Status          string
	Title           string
	Description     *string
	FixInstructions *string
	FixPayload      []byte
	ResolvedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type IssuesRepo struct{}

func (IssuesRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, i *Issue) error {
	i.TenantID = tenantID
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO issues
		  (tenant_id, audit_id, store_id, product_id, product_title,
		   rule_code, severity, title, description, fix_instructions, fix_payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7::issue_severity, $8, $9, $10, $11)
		RETURNING id, status::text, created_at, updated_at
	`, tenantID, i.AuditID, i.StoreID, i.ProductID, i.ProductTitle,
		i.RuleCode, i.Severity, i.Title, i.Description, i.FixInstructions, i.FixPayload,
	).Scan(&i.ID, &i.Status, &i.CreatedAt, &i.UpdatedAt))
}

func (IssuesRepo) ListByAudit(ctx context.Context, q Querier, tenantID, auditID uuid.UUID) ([]Issue, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, audit_id, store_id, product_id, product_title,
		       rule_code, severity::text, status::text, title, description,
		       fix_instructions, fix_payload, resolved_at, created_at, updated_at
		FROM issues
		WHERE tenant_id=$1 AND audit_id=$2
		ORDER BY severity, rule_code
	`, tenantID, auditID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		var i Issue
		if err := rows.Scan(&i.ID, &i.TenantID, &i.AuditID, &i.StoreID, &i.ProductID, &i.ProductTitle,
			&i.RuleCode, &i.Severity, &i.Status, &i.Title, &i.Description,
			&i.FixInstructions, &i.FixPayload, &i.ResolvedAt, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (IssuesRepo) MarkStatus(ctx context.Context, q Querier, tenantID, id uuid.UUID, status string, at *time.Time) error {
	tag, err := q.Exec(ctx, `
		UPDATE issues
		SET status=$3::issue_status,
		    resolved_at = CASE WHEN $3='fixed' THEN $4 ELSE resolved_at END,
		    updated_at=now()
		WHERE tenant_id=$1 AND id=$2
	`, tenantID, id, status, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
