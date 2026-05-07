package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type PasswordResetToken struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	TokenHash   []byte
	RequestedIP *string
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
	CreatedAt   time.Time
}

type PasswordResetTokensRepo struct{}

func (PasswordResetTokensRepo) Insert(ctx context.Context, q Querier, t *PasswordResetToken) error {
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO password_reset_tokens (user_id, token_hash, requested_ip, expires_at)
		VALUES ($1, $2, $3::inet, $4)
		RETURNING id, created_at
	`, t.UserID, t.TokenHash, t.RequestedIP, t.ExpiresAt).Scan(&t.ID, &t.CreatedAt))
}

func (PasswordResetTokensRepo) GetActiveByTokenHash(ctx context.Context, q Querier, hash []byte, now time.Time) (*PasswordResetToken, error) {
	t := &PasswordResetToken{}
	err := q.QueryRow(ctx, `
		SELECT id, user_id, token_hash, host(requested_ip), expires_at, consumed_at, created_at
		FROM password_reset_tokens
		WHERE token_hash=$1 AND expires_at > $2 AND consumed_at IS NULL
	`, hash, now).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.RequestedIP, &t.ExpiresAt, &t.ConsumedAt, &t.CreatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return t, nil
}

func (PasswordResetTokensRepo) Consume(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE password_reset_tokens SET consumed_at=$2 WHERE id=$1 AND consumed_at IS NULL`,
		id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
