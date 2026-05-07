package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type PlatformAdminAuditLogEntry struct {
	ID          uuid.UUID
	AdminUserID *uuid.UUID
	Action      string
	TargetType  *string
	TargetID    *string
	Metadata    []byte
	IPAddress   *string
	CreatedAt   time.Time
}

type PlatformAdminAuditLogRepo struct{}

func (PlatformAdminAuditLogRepo) Insert(ctx context.Context, q Querier, e *PlatformAdminAuditLogEntry) error {
	if len(e.Metadata) == 0 {
		e.Metadata = []byte("{}")
	}
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO platform_admin_audit_log
		  (admin_user_id, action, target_type, target_id, metadata, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6::inet)
		RETURNING id, created_at
	`, e.AdminUserID, e.Action, e.TargetType, e.TargetID, e.Metadata, e.IPAddress,
	).Scan(&e.ID, &e.CreatedAt))
}

func (PlatformAdminAuditLogRepo) ListByAdmin(ctx context.Context, q Querier, adminUserID uuid.UUID, limit int) ([]PlatformAdminAuditLogEntry, error) {
	rows, err := q.Query(ctx, `
		SELECT id, admin_user_id, action, target_type, target_id, metadata,
		       host(ip_address), created_at
		FROM platform_admin_audit_log
		WHERE admin_user_id=$1
		ORDER BY created_at DESC
		LIMIT $2
	`, adminUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlatformAdminAuditLogEntry
	for rows.Next() {
		var e PlatformAdminAuditLogEntry
		if err := rows.Scan(&e.ID, &e.AdminUserID, &e.Action, &e.TargetType, &e.TargetID,
			&e.Metadata, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
