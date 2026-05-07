package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrSessionNotFound = errors.New("auth: session not found")
	ErrSessionExpired  = errors.New("auth: session expired")
)

type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Token      string
	IPAddress  string
	UserAgent  string
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

type sessionRow struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TokenHash  []byte
	IPAddress  string
	UserAgent  string
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

type SessionDB interface {
	Insert(ctx context.Context, row sessionRow) error
	FindActiveByTokenHash(ctx context.Context, tokenHash []byte, now time.Time) (sessionRow, error)
	UpdateExpiry(ctx context.Context, id uuid.UUID, newExpires, newLastSeen time.Time) error
	Revoke(ctx context.Context, id uuid.UUID, revokedAt time.Time) error
}

type SessionStore struct {
	db  SessionDB
	ttl time.Duration
	now func() time.Time
}

func NewSessionStore(db SessionDB, ttl time.Duration) *SessionStore {
	return &SessionStore{db: db, ttl: ttl, now: time.Now}
}

func (s *SessionStore) WithClock(now func() time.Time) *SessionStore {
	c := *s
	c.now = now
	return &c
}

func (s *SessionStore) Create(ctx context.Context, userID uuid.UUID, ip, ua string) (Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, fmt.Errorf("read token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))

	now := s.now()
	row := sessionRow{
		ID:         uuid.New(),
		UserID:     userID,
		TokenHash:  hash[:],
		IPAddress:  ip,
		UserAgent:  ua,
		ExpiresAt:  now.Add(s.ttl),
		LastSeenAt: now,
		CreatedAt:  now,
	}
	if err := s.db.Insert(ctx, row); err != nil {
		return Session{}, err
	}
	return rowToSession(row, token), nil
}

func (s *SessionStore) Get(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrSessionNotFound
	}
	hash := sha256.Sum256([]byte(token))
	row, err := s.db.FindActiveByTokenHash(ctx, hash[:], s.now())
	if err != nil {
		return Session{}, err
	}
	return rowToSession(row, token), nil
}

func (s *SessionStore) Extend(ctx context.Context, id uuid.UUID) error {
	now := s.now()
	return s.db.UpdateExpiry(ctx, id, now.Add(s.ttl), now)
}

func (s *SessionStore) Revoke(ctx context.Context, id uuid.UUID) error {
	return s.db.Revoke(ctx, id, s.now())
}

func rowToSession(r sessionRow, token string) Session {
	return Session{
		ID:         r.ID,
		UserID:     r.UserID,
		Token:      token,
		IPAddress:  r.IPAddress,
		UserAgent:  r.UserAgent,
		ExpiresAt:  r.ExpiresAt,
		LastSeenAt: r.LastSeenAt,
		RevokedAt:  r.RevokedAt,
		CreatedAt:  r.CreatedAt,
	}
}
