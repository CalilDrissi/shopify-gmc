package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type GmcAccountSnapshot struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	GmcConnectionID uuid.UUID
	CapturedAt      time.Time
	ProductCount    *int
	RawData         []byte
	CreatedAt       time.Time
}

type GmcAccountSnapshotsRepo struct{}

func (GmcAccountSnapshotsRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, s *GmcAccountSnapshot) error {
	s.TenantID = tenantID
	if len(s.RawData) == 0 {
		s.RawData = []byte("{}")
	}
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO gmc_account_snapshots
		  (tenant_id, gmc_connection_id, captured_at, product_count, raw_data)
		VALUES ($1, $2, COALESCE($3, now()), $4, $5)
		RETURNING id, captured_at, created_at
	`, tenantID, s.GmcConnectionID, s.CapturedAt, s.ProductCount, s.RawData,
	).Scan(&s.ID, &s.CapturedAt, &s.CreatedAt))
}

func (GmcAccountSnapshotsRepo) ListByConnection(ctx context.Context, q Querier, tenantID, connID uuid.UUID, limit int) ([]GmcAccountSnapshot, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, gmc_connection_id, captured_at, product_count, raw_data, created_at
		FROM gmc_account_snapshots
		WHERE tenant_id=$1 AND gmc_connection_id=$2
		ORDER BY captured_at DESC
		LIMIT $3
	`, tenantID, connID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GmcAccountSnapshot
	for rows.Next() {
		var s GmcAccountSnapshot
		if err := rows.Scan(&s.ID, &s.TenantID, &s.GmcConnectionID, &s.CapturedAt,
			&s.ProductCount, &s.RawData, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
