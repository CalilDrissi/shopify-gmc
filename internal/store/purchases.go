package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Purchase struct {
	ID            uuid.UUID
	TenantID      *uuid.UUID
	UserID        *uuid.UUID
	GumroadSaleID *string
	LicenseKey    *string
	ProductID     *string
	Plan          string
	AmountCents   *int
	Currency      *string
	Status        string
	PurchasedAt   *time.Time
	RefundedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type PurchasesRepo struct{}

func (PurchasesRepo) Insert(ctx context.Context, q Querier, p *Purchase) error {
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO purchases
		  (tenant_id, user_id, gumroad_sale_id, license_key, product_id,
		   plan, amount_cents, currency, purchased_at)
		VALUES ($1, $2, $3, $4, $5, $6::plan_tier, $7, $8, $9)
		RETURNING id, status::text, created_at, updated_at
	`, p.TenantID, p.UserID, p.GumroadSaleID, p.LicenseKey, p.ProductID,
		p.Plan, p.AmountCents, p.Currency, p.PurchasedAt,
	).Scan(&p.ID, &p.Status, &p.CreatedAt, &p.UpdatedAt))
}

func (PurchasesRepo) GetByLicense(ctx context.Context, q Querier, license string) (*Purchase, error) {
	p := &Purchase{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, gumroad_sale_id, license_key, product_id,
		       plan::text, amount_cents, currency, status::text, purchased_at,
		       refunded_at, created_at, updated_at
		FROM purchases WHERE license_key=$1
	`, license).Scan(&p.ID, &p.TenantID, &p.UserID, &p.GumroadSaleID, &p.LicenseKey, &p.ProductID,
		&p.Plan, &p.AmountCents, &p.Currency, &p.Status, &p.PurchasedAt,
		&p.RefundedAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return p, nil
}

func (PurchasesRepo) ListByTenant(ctx context.Context, q Querier, tenantID uuid.UUID) ([]Purchase, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, user_id, gumroad_sale_id, license_key, product_id,
		       plan::text, amount_cents, currency, status::text, purchased_at,
		       refunded_at, created_at, updated_at
		FROM purchases
		WHERE tenant_id=$1
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Purchase
	for rows.Next() {
		var p Purchase
		if err := rows.Scan(&p.ID, &p.TenantID, &p.UserID, &p.GumroadSaleID, &p.LicenseKey, &p.ProductID,
			&p.Plan, &p.AmountCents, &p.Currency, &p.Status, &p.PurchasedAt,
			&p.RefundedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (PurchasesRepo) MarkRefunded(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE purchases SET status='refunded', refunded_at=$2, updated_at=now() WHERE id=$1`,
		id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
