package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type EmailVerificationToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Email      string
	TokenHash  []byte
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

type EmailVerificationTokensRepo struct{}

func (EmailVerificationTokensRepo) Insert(ctx context.Context, q Querier, t *EmailVerificationToken) error {
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO email_verification_tokens (user_id, email, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at
	`, t.UserID, t.Email, t.TokenHash, t.ExpiresAt).Scan(&t.ID, &t.CreatedAt))
}

func (EmailVerificationTokensRepo) GetActiveByTokenHash(ctx context.Context, q Querier, hash []byte, now time.Time) (*EmailVerificationToken, error) {
	t := &EmailVerificationToken{}
	err := q.QueryRow(ctx, `
		SELECT id, user_id, email, token_hash, expires_at, consumed_at, created_at
		FROM email_verification_tokens
		WHERE token_hash=$1 AND expires_at > $2 AND consumed_at IS NULL
	`, hash, now).Scan(&t.ID, &t.UserID, &t.Email, &t.TokenHash, &t.ExpiresAt, &t.ConsumedAt, &t.CreatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return t, nil
}

func (EmailVerificationTokensRepo) Consume(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE email_verification_tokens SET consumed_at=$2 WHERE id=$1 AND consumed_at IS NULL`,
		id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
