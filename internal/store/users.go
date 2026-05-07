package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID
	Email           string
	EmailVerifiedAt *time.Time
	PasswordHash    string
	Name            *string
	DefaultTenantID *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type UsersRepo struct{}

func (UsersRepo) Insert(ctx context.Context, q Querier, u *User) error {
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, default_tenant_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`, u.Email, u.PasswordHash, u.Name, u.DefaultTenantID).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt))
}

func (UsersRepo) GetByID(ctx context.Context, q Querier, id uuid.UUID) (*User, error) {
	u := &User{}
	err := q.QueryRow(ctx, `
		SELECT id, email, email_verified_at, password_hash, name, default_tenant_id, created_at, updated_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.PasswordHash, &u.Name, &u.DefaultTenantID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return u, nil
}

func (UsersRepo) GetByEmail(ctx context.Context, q Querier, email string) (*User, error) {
	u := &User{}
	err := q.QueryRow(ctx, `
		SELECT id, email, email_verified_at, password_hash, name, default_tenant_id, created_at, updated_at
		FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.PasswordHash, &u.Name, &u.DefaultTenantID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return u, nil
}

func (UsersRepo) UpdatePassword(ctx context.Context, q Querier, id uuid.UUID, newHash string) error {
	tag, err := q.Exec(ctx, `UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, id, newHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (UsersRepo) MarkEmailVerified(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx, `UPDATE users SET email_verified_at=$2, updated_at=now() WHERE id=$1`, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (UsersRepo) SetDefaultTenant(ctx context.Context, q Querier, userID uuid.UUID, tenantID *uuid.UUID) error {
	tag, err := q.Exec(ctx, `UPDATE users SET default_tenant_id=$2, updated_at=now() WHERE id=$1`, userID, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type MembershipWithTenant struct {
	TenantID  uuid.UUID
	Slug      string
	Name      string
	Plan      string
	Role      string
	IsDefault bool
}

func (UsersRepo) ListMemberships(ctx context.Context, q Querier, userID uuid.UUID) ([]MembershipWithTenant, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, t.slug, t.name, t.plan::text, m.role::text,
		       (u.default_tenant_id = t.id) AS is_default
		FROM memberships m
		JOIN tenants t ON t.id = m.tenant_id
		JOIN users u   ON u.id = m.user_id
		WHERE m.user_id = $1
		ORDER BY m.created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MembershipWithTenant
	for rows.Next() {
		var m MembershipWithTenant
		if err := rows.Scan(&m.TenantID, &m.Slug, &m.Name, &m.Plan, &m.Role, &m.IsDefault); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
