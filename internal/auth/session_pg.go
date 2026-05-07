package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type pgSessionDB struct {
	db *sql.DB
}

func NewPostgresSessionDB(db *sql.DB) SessionDB {
	return &pgSessionDB{db: db}
}

func (p *pgSessionDB) Insert(ctx context.Context, r sessionRow) error {
	var ip any
	if r.IPAddress != "" {
		ip = r.IPAddress
	}
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO sessions
		  (id, user_id, token_hash, ip_address, user_agent, expires_at, last_seen_at, created_at)
		VALUES
		  ($1, $2, $3, $4::inet, NULLIF($5,''), $6, $7, $8)
	`, r.ID, r.UserID, r.TokenHash, ip, r.UserAgent, r.ExpiresAt, r.LastSeenAt, r.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (p *pgSessionDB) FindActiveByTokenHash(ctx context.Context, hash []byte, now time.Time) (sessionRow, error) {
	var (
		r   sessionRow
		ip  sql.NullString
		ua  sql.NullString
		rev sql.NullTime
	)
	err := p.db.QueryRowContext(ctx, `
		SELECT id, user_id, token_hash, host(ip_address), user_agent,
		       expires_at, last_seen_at, revoked_at, created_at
		FROM sessions
		WHERE token_hash = $1 AND expires_at > $2 AND revoked_at IS NULL
	`, hash, now).Scan(
		&r.ID, &r.UserID, &r.TokenHash, &ip, &ua,
		&r.ExpiresAt, &r.LastSeenAt, &rev, &r.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRow{}, ErrSessionNotFound
	}
	if err != nil {
		return sessionRow{}, fmt.Errorf("find session: %w", err)
	}
	r.IPAddress = ip.String
	r.UserAgent = ua.String
	if rev.Valid {
		t := rev.Time
		r.RevokedAt = &t
	}
	return r, nil
}

func (p *pgSessionDB) UpdateExpiry(ctx context.Context, id uuid.UUID, expires, lastSeen time.Time) error {
	res, err := p.db.ExecContext(ctx, `
		UPDATE sessions SET expires_at = $2, last_seen_at = $3
		WHERE id = $1 AND revoked_at IS NULL
	`, id, expires, lastSeen)
	if err != nil {
		return fmt.Errorf("update session expiry: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (p *pgSessionDB) Revoke(ctx context.Context, id uuid.UUID, revokedAt time.Time) error {
	res, err := p.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = $2
		WHERE id = $1 AND revoked_at IS NULL
	`, id, revokedAt)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}
