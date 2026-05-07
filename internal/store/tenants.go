package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Tenant struct {
	ID           uuid.UUID
	Name         string
	Slug         string
	Kind         string
	Plan         string
	PlanRenewsAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type TenantsRepo struct{}

func (TenantsRepo) Insert(ctx context.Context, q Querier, t *Tenant) error {
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, kind, plan)
		VALUES ($1, $2, COALESCE(NULLIF($3,'')::tenant_kind, 'individual'),
		                COALESCE(NULLIF($4,'')::plan_tier,   'free'))
		RETURNING id, kind::text, plan::text, created_at, updated_at
	`, t.Name, t.Slug, t.Kind, t.Plan).Scan(&t.ID, &t.Kind, &t.Plan, &t.CreatedAt, &t.UpdatedAt))
}

func (TenantsRepo) GetByID(ctx context.Context, q Querier, id uuid.UUID) (*Tenant, error) {
	t := &Tenant{}
	err := q.QueryRow(ctx, `
		SELECT id, name, slug, kind::text, plan::text, plan_renews_at, created_at, updated_at
		FROM tenants WHERE id=$1
	`, id).Scan(&t.ID, &t.Name, &t.Slug, &t.Kind, &t.Plan, &t.PlanRenewsAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return t, nil
}

func (TenantsRepo) GetBySlug(ctx context.Context, q Querier, slug string) (*Tenant, error) {
	t := &Tenant{}
	err := q.QueryRow(ctx, `
		SELECT id, name, slug, kind::text, plan::text, plan_renews_at, created_at, updated_at
		FROM tenants WHERE slug=$1
	`, slug).Scan(&t.ID, &t.Name, &t.Slug, &t.Kind, &t.Plan, &t.PlanRenewsAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return t, nil
}

func (TenantsRepo) UpdatePlan(ctx context.Context, q Querier, id uuid.UUID, plan string, renewsAt *time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE tenants SET plan=$2::plan_tier, plan_renews_at=$3, updated_at=now() WHERE id=$1`,
		id, plan, renewsAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
