package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ImpersonationLogEntry struct {
	ID                 uuid.UUID
	AdminUserID        *uuid.UUID
	ImpersonatedUserID *uuid.UUID
	TenantID           *uuid.UUID
	SessionID          *uuid.UUID
	StartedAt          time.Time
	EndedAt            *time.Time
	Reason             *string
}

type ImpersonationLogRepo struct{}

func (ImpersonationLogRepo) Start(ctx context.Context, q Querier, e *ImpersonationLogEntry) error {
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO impersonation_log
		  (admin_user_id, impersonated_user_id, tenant_id, session_id, reason)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, started_at
	`, e.AdminUserID, e.ImpersonatedUserID, e.TenantID, e.SessionID, e.Reason,
	).Scan(&e.ID, &e.StartedAt))
}

func (ImpersonationLogRepo) End(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE impersonation_log SET ended_at=$2 WHERE id=$1 AND ended_at IS NULL`, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (ImpersonationLogRepo) ListByAdmin(ctx context.Context, q Querier, adminUserID uuid.UUID, limit int) ([]ImpersonationLogEntry, error) {
	rows, err := q.Query(ctx, `
		SELECT id, admin_user_id, impersonated_user_id, tenant_id, session_id,
		       started_at, ended_at, reason
		FROM impersonation_log
		WHERE admin_user_id=$1
		ORDER BY started_at DESC
		LIMIT $2
	`, adminUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImpersonationLogEntry
	for rows.Next() {
		var e ImpersonationLogEntry
		if err := rows.Scan(&e.ID, &e.AdminUserID, &e.ImpersonatedUserID, &e.TenantID,
			&e.SessionID, &e.StartedAt, &e.EndedAt, &e.Reason); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
