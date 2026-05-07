package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Membership struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    uuid.UUID
	Role      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type MembershipsRepo struct{}

func (MembershipsRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, m *Membership) error {
	m.TenantID = tenantID
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO memberships (tenant_id, user_id, role)
		VALUES ($1, $2, $3::membership_role)
		RETURNING id, role::text, created_at, updated_at
	`, tenantID, m.UserID, m.Role).Scan(&m.ID, &m.Role, &m.CreatedAt, &m.UpdatedAt))
}

func (MembershipsRepo) ListByTenant(ctx context.Context, q Querier, tenantID uuid.UUID) ([]Membership, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, user_id, role::text, created_at, updated_at
		FROM memberships
		WHERE tenant_id=$1
		ORDER BY created_at
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (MembershipsRepo) GetByTenantAndUser(ctx context.Context, q Querier, tenantID, userID uuid.UUID) (*Membership, error) {
	m := &Membership{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, role::text, created_at, updated_at
		FROM memberships
		WHERE tenant_id=$1 AND user_id=$2
	`, tenantID, userID).Scan(&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return m, nil
}

func (MembershipsRepo) Remove(ctx context.Context, q Querier, tenantID, userID uuid.UUID) error {
	tag, err := q.Exec(ctx, `DELETE FROM memberships WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
