package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TokenHash  []byte
	IPAddress  *string
	UserAgent  *string
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

type SessionsRepo struct{}

func (SessionsRepo) Insert(ctx context.Context, q Querier, s *Session) error {
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO sessions
		  (user_id, token_hash, ip_address, user_agent, expires_at, last_seen_at)
		VALUES ($1, $2, $3::inet, $4, $5, COALESCE($6, now()))
		RETURNING id, created_at, last_seen_at
	`, s.UserID, s.TokenHash, s.IPAddress, s.UserAgent, s.ExpiresAt, s.LastSeenAt,
	).Scan(&s.ID, &s.CreatedAt, &s.LastSeenAt))
}

func (SessionsRepo) GetActiveByTokenHash(ctx context.Context, q Querier, hash []byte, now time.Time) (*Session, error) {
	s := &Session{}
	err := q.QueryRow(ctx, `
		SELECT id, user_id, token_hash, host(ip_address), user_agent,
		       expires_at, last_seen_at, revoked_at, created_at
		FROM sessions
		WHERE token_hash = $1 AND expires_at > $2 AND revoked_at IS NULL
	`, hash, now).Scan(
		&s.ID, &s.UserID, &s.TokenHash, &s.IPAddress, &s.UserAgent,
		&s.ExpiresAt, &s.LastSeenAt, &s.RevokedAt, &s.CreatedAt,
	)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return s, nil
}

func (SessionsRepo) ListActiveByUser(ctx context.Context, q Querier, userID uuid.UUID, now time.Time) ([]Session, error) {
	rows, err := q.Query(ctx, `
		SELECT id, user_id, token_hash, host(ip_address), user_agent,
		       expires_at, last_seen_at, revoked_at, created_at
		FROM sessions
		WHERE user_id = $1 AND expires_at > $2 AND revoked_at IS NULL
		ORDER BY last_seen_at DESC
	`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.IPAddress, &s.UserAgent,
			&s.ExpiresAt, &s.LastSeenAt, &s.RevokedAt, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (SessionsRepo) Extend(ctx context.Context, q Querier, id uuid.UUID, newExpires, lastSeen time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE sessions SET expires_at=$2, last_seen_at=$3 WHERE id=$1 AND revoked_at IS NULL`,
		id, newExpires, lastSeen)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (SessionsRepo) Revoke(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE sessions SET revoked_at=$2 WHERE id=$1 AND revoked_at IS NULL`, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (SessionsRepo) RevokeAllForUser(ctx context.Context, q Querier, userID uuid.UUID, at time.Time) (int64, error) {
	tag, err := q.Exec(ctx,
		`UPDATE sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, at)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
