package settings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/store"
)

// postgresBackend persists encrypted blobs through the existing
// platform_settings (jsonb) repository by wrapping the bytes as a base64 JSON
// string envelope.
type postgresBackend struct {
	pool *pgxpool.Pool
	repo store.PlatformSettingsRepo
}

func NewPostgresBackend(pool *pgxpool.Pool) Backend {
	return &postgresBackend{pool: pool}
}

func (p *postgresBackend) Get(ctx context.Context, key string) ([]byte, error) {
	s, err := p.repo.Get(ctx, p.pool, key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var b64 string
	if err := json.Unmarshal(s.Value, &b64); err != nil {
		return nil, fmt.Errorf("settings: decode envelope: %w", err)
	}
	return base64.StdEncoding.DecodeString(b64)
}

func (p *postgresBackend) Set(ctx context.Context, key string, value []byte) error {
	encoded, err := json.Marshal(base64.StdEncoding.EncodeToString(value))
	if err != nil {
		return fmt.Errorf("settings: encode envelope: %w", err)
	}
	return p.repo.Set(ctx, p.pool, key, encoded)
}

func (p *postgresBackend) Delete(ctx context.Context, key string) error {
	if err := p.repo.Delete(ctx, p.pool, key); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
