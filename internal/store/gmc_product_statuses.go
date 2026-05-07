package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type GmcProductStatus struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	StoreID             uuid.UUID
	GmcConnectionID     *uuid.UUID
	ProductID           string
	GmcItemID           *string
	ApprovalStatus      string
	DestinationStatuses []byte
	Issues              []byte
	LastCheckedAt       time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type GmcProductStatusesRepo struct{}

func (GmcProductStatusesRepo) Upsert(ctx context.Context, q Querier, tenantID uuid.UUID, p *GmcProductStatus) error {
	p.TenantID = tenantID
	if len(p.DestinationStatuses) == 0 {
		p.DestinationStatuses = []byte("[]")
	}
	if len(p.Issues) == 0 {
		p.Issues = []byte("[]")
	}
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO gmc_product_statuses
		  (tenant_id, store_id, gmc_connection_id, product_id, gmc_item_id,
		   approval_status, destination_statuses, issues, last_checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()))
		ON CONFLICT (store_id, product_id) DO UPDATE
		  SET gmc_connection_id    = EXCLUDED.gmc_connection_id,
		      gmc_item_id          = EXCLUDED.gmc_item_id,
		      approval_status      = EXCLUDED.approval_status,
		      destination_statuses = EXCLUDED.destination_statuses,
		      issues               = EXCLUDED.issues,
		      last_checked_at      = EXCLUDED.last_checked_at,
		      updated_at           = now()
		RETURNING id, last_checked_at, created_at, updated_at
	`, tenantID, p.StoreID, p.GmcConnectionID, p.ProductID, p.GmcItemID,
		p.ApprovalStatus, p.DestinationStatuses, p.Issues, p.LastCheckedAt,
	).Scan(&p.ID, &p.LastCheckedAt, &p.CreatedAt, &p.UpdatedAt))
}

func (GmcProductStatusesRepo) ListByStore(ctx context.Context, q Querier, tenantID, storeID uuid.UUID) ([]GmcProductStatus, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, store_id, gmc_connection_id, product_id, gmc_item_id,
		       approval_status, destination_statuses, issues, last_checked_at, created_at, updated_at
		FROM gmc_product_statuses
		WHERE tenant_id=$1 AND store_id=$2
		ORDER BY product_id
	`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GmcProductStatus
	for rows.Next() {
		var p GmcProductStatus
		if err := rows.Scan(&p.ID, &p.TenantID, &p.StoreID, &p.GmcConnectionID, &p.ProductID,
			&p.GmcItemID, &p.ApprovalStatus, &p.DestinationStatuses, &p.Issues,
			&p.LastCheckedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (GmcProductStatusesRepo) CountByApproval(ctx context.Context, q Querier, tenantID, storeID uuid.UUID) (map[string]int, error) {
	rows, err := q.Query(ctx, `
		SELECT approval_status, count(*)::int
		FROM gmc_product_statuses
		WHERE tenant_id=$1 AND store_id=$2
		GROUP BY approval_status
	`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}
