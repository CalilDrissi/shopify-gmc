package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type AuditDiff struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	AuditID            uuid.UUID
	PreviousAuditID    *uuid.UUID
	NewIssueCount      int
	ResolvedIssueCount int
	Diff               []byte
	CreatedAt          time.Time
}

type AuditDiffsRepo struct{}

func (AuditDiffsRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, d *AuditDiff) error {
	d.TenantID = tenantID
	if len(d.Diff) == 0 {
		d.Diff = []byte("{}")
	}
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO audit_diffs
		  (tenant_id, audit_id, previous_audit_id, new_issue_count, resolved_issue_count, diff)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, tenantID, d.AuditID, d.PreviousAuditID, d.NewIssueCount, d.ResolvedIssueCount, d.Diff,
	).Scan(&d.ID, &d.CreatedAt))
}

func (AuditDiffsRepo) GetByAudit(ctx context.Context, q Querier, tenantID, auditID uuid.UUID) (*AuditDiff, error) {
	d := &AuditDiff{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, audit_id, previous_audit_id, new_issue_count,
		       resolved_issue_count, diff, created_at
		FROM audit_diffs
		WHERE tenant_id=$1 AND audit_id=$2
	`, tenantID, auditID).Scan(&d.ID, &d.TenantID, &d.AuditID, &d.PreviousAuditID,
		&d.NewIssueCount, &d.ResolvedIssueCount, &d.Diff, &d.CreatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return d, nil
}
