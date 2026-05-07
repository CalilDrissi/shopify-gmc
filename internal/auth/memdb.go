package auth

import (
	"context"
	"encoding/hex"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MemSessionDB struct {
	mu     sync.Mutex
	byHash map[string]*sessionRow
	byID   map[uuid.UUID]*sessionRow
}

func NewMemSessionDB() *MemSessionDB {
	return &MemSessionDB{
		byHash: map[string]*sessionRow{},
		byID:   map[uuid.UUID]*sessionRow{},
	}
}

func (m *MemSessionDB) Insert(_ context.Context, r sessionRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := r
	m.byHash[hex.EncodeToString(r.TokenHash)] = &cp
	m.byID[r.ID] = &cp
	return nil
}

func (m *MemSessionDB) FindActiveByTokenHash(_ context.Context, hash []byte, now time.Time) (sessionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.byHash[hex.EncodeToString(hash)]
	if !ok || row.RevokedAt != nil || !row.ExpiresAt.After(now) {
		return sessionRow{}, ErrSessionNotFound
	}
	return *row, nil
}

func (m *MemSessionDB) UpdateExpiry(_ context.Context, id uuid.UUID, expires, lastSeen time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.byID[id]
	if !ok || row.RevokedAt != nil {
		return ErrSessionNotFound
	}
	row.ExpiresAt = expires
	row.LastSeenAt = lastSeen
	return nil
}

func (m *MemSessionDB) Revoke(_ context.Context, id uuid.UUID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.byID[id]
	if !ok || row.RevokedAt != nil {
		return ErrSessionNotFound
	}
	row.RevokedAt = &at
	return nil
}
