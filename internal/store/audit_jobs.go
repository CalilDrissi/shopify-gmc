package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AuditJob struct {
	ID          uuid.UUID
	TenantID    *uuid.UUID
	Kind        string
	Payload     []byte
	Status      string
	RunAt       time.Time
	Attempts    int
	MaxAttempts int
	LastError   *string
	LockedAt    *time.Time
	LockedBy    *string
	FinishedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AuditJobsRepo struct{}

func (AuditJobsRepo) Enqueue(ctx context.Context, q Querier, tenantID *uuid.UUID, kind string, payload []byte, runAt time.Time) (*AuditJob, error) {
	j := &AuditJob{TenantID: tenantID, Kind: kind, Payload: payload, RunAt: runAt}
	if len(j.Payload) == 0 {
		j.Payload = []byte("{}")
	}
	err := q.QueryRow(ctx, `
		INSERT INTO audit_jobs (tenant_id, kind, payload, run_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, status::text, attempts, max_attempts, created_at, updated_at
	`, tenantID, kind, j.Payload, runAt).Scan(
		&j.ID, &j.Status, &j.Attempts, &j.MaxAttempts, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return j, nil
}

// ClaimNext locks the next ready job using FOR UPDATE SKIP LOCKED.
func (AuditJobsRepo) ClaimNext(ctx context.Context, q Querier, worker string, now time.Time) (*AuditJob, error) {
	j := &AuditJob{}
	err := q.QueryRow(ctx, `
		WITH next AS (
		  SELECT id FROM audit_jobs
		  WHERE status='queued' AND run_at <= $2
		  ORDER BY run_at
		  FOR UPDATE SKIP LOCKED
		  LIMIT 1
		)
		UPDATE audit_jobs aj
		SET status='running', locked_at=$2, locked_by=$1, attempts=aj.attempts+1, updated_at=now()
		FROM next
		WHERE aj.id = next.id
		RETURNING aj.id, aj.tenant_id, aj.kind, aj.payload, aj.status::text, aj.run_at,
		          aj.attempts, aj.max_attempts, aj.last_error, aj.locked_at, aj.locked_by,
		          aj.finished_at, aj.created_at, aj.updated_at
	`, worker, now).Scan(&j.ID, &j.TenantID, &j.Kind, &j.Payload, &j.Status, &j.RunAt,
		&j.Attempts, &j.MaxAttempts, &j.LastError, &j.LockedAt, &j.LockedBy,
		&j.FinishedAt, &j.CreatedAt, &j.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (AuditJobsRepo) MarkSucceeded(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE audit_jobs SET status='succeeded', finished_at=$2, updated_at=now() WHERE id=$1`,
		id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (AuditJobsRepo) MarkFailed(ctx context.Context, q Querier, id uuid.UUID, errMsg string, retryAt *time.Time) error {
	if retryAt == nil {
		_, err := q.Exec(ctx,
			`UPDATE audit_jobs SET status='dead', last_error=$2, finished_at=now(), updated_at=now() WHERE id=$1`,
			id, errMsg)
		return err
	}
	_, err := q.Exec(ctx, `
		UPDATE audit_jobs
		SET status='queued', last_error=$2, run_at=$3, locked_at=NULL, locked_by=NULL, updated_at=now()
		WHERE id=$1
	`, id, errMsg, *retryAt)
	return err
}
