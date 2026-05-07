package store

import (
	"context"
	"time"
)

type PlatformSetting struct {
	Key       string
	Value     []byte
	UpdatedAt time.Time
}

type PlatformSettingsRepo struct{}

func (PlatformSettingsRepo) Get(ctx context.Context, q Querier, key string) (*PlatformSetting, error) {
	s := &PlatformSetting{Key: key}
	err := q.QueryRow(ctx,
		`SELECT key, value, updated_at FROM platform_settings WHERE key=$1`, key,
	).Scan(&s.Key, &s.Value, &s.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return s, nil
}

func (PlatformSettingsRepo) Set(ctx context.Context, q Querier, key string, value []byte) error {
	_, err := q.Exec(ctx, `
		INSERT INTO platform_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()
	`, key, value)
	return err
}

func (PlatformSettingsRepo) Delete(ctx context.Context, q Querier, key string) error {
	tag, err := q.Exec(ctx, `DELETE FROM platform_settings WHERE key=$1`, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
