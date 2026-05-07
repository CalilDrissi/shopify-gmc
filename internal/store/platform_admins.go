package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PlatformAdmin struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	Role            string
	TOTPSecret      *string
	TOTPEnrolledAt  *time.Time
	CreatedAt       time.Time
}

type PlatformAdminsRepo struct{}

func (PlatformAdminsRepo) Grant(ctx context.Context, q Querier, userID uuid.UUID, role string) (*PlatformAdmin, error) {
	a := &PlatformAdmin{}
	err := q.QueryRow(ctx, `
		INSERT INTO platform_admins (user_id, role)
		VALUES ($1, COALESCE(NULLIF($2,'')::platform_admin_role, 'admin'))
		ON CONFLICT (user_id) DO UPDATE SET role = EXCLUDED.role
		RETURNING id, user_id, role::text, created_at
	`, userID, role).Scan(&a.ID, &a.UserID, &a.Role, &a.CreatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return a, nil
}

func (PlatformAdminsRepo) Revoke(ctx context.Context, q Querier, userID uuid.UUID) error {
	tag, err := q.Exec(ctx, `DELETE FROM platform_admins WHERE user_id=$1`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (PlatformAdminsRepo) IsPlatformAdmin(ctx context.Context, q Querier, userID uuid.UUID) (bool, error) {
	var one int
	err := q.QueryRow(ctx, `SELECT 1 FROM platform_admins WHERE user_id=$1`, userID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (PlatformAdminsRepo) GetByUserID(ctx context.Context, q Querier, userID uuid.UUID) (*PlatformAdmin, error) {
	a := &PlatformAdmin{}
	err := q.QueryRow(ctx, `
		SELECT id, user_id, role::text, totp_secret, totp_enrolled_at, created_at
		FROM platform_admins WHERE user_id=$1
	`, userID).Scan(&a.ID, &a.UserID, &a.Role, &a.TOTPSecret, &a.TOTPEnrolledAt, &a.CreatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return a, nil
}

func (PlatformAdminsRepo) SetTOTPSecret(ctx context.Context, q Querier, userID uuid.UUID, secret string, enrolledAt time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE platform_admins SET totp_secret=$2, totp_enrolled_at=$3 WHERE user_id=$1`,
		userID, secret, enrolledAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (PlatformAdminsRepo) ListAll(ctx context.Context, q Querier) ([]PlatformAdmin, error) {
	rows, err := q.Query(ctx, `
		SELECT id, user_id, role::text, totp_secret, totp_enrolled_at, created_at
		FROM platform_admins ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlatformAdmin
	for rows.Next() {
		var a PlatformAdmin
		if err := rows.Scan(&a.ID, &a.UserID, &a.Role, &a.TOTPSecret, &a.TOTPEnrolledAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
