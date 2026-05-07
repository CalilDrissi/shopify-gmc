package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Invitation struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	InviterID  *uuid.UUID
	Email      string
	Role       string
	TokenHash  []byte
	Status     string
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	CreatedAt  time.Time
}

type InvitationsRepo struct{}

func (InvitationsRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, inv *Invitation) error {
	inv.TenantID = tenantID
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO invitations (tenant_id, inviter_id, email, role, token_hash, expires_at)
		VALUES ($1, $2, $3, $4::membership_role, $5, $6)
		RETURNING id, status::text, created_at
	`, tenantID, inv.InviterID, inv.Email, inv.Role, inv.TokenHash, inv.ExpiresAt,
	).Scan(&inv.ID, &inv.Status, &inv.CreatedAt))
}

func (InvitationsRepo) GetByTokenHash(ctx context.Context, q Querier, hash []byte) (*Invitation, error) {
	i := &Invitation{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, inviter_id, email, role::text, token_hash, status::text,
		       expires_at, accepted_at, created_at
		FROM invitations WHERE token_hash=$1
	`, hash).Scan(&i.ID, &i.TenantID, &i.InviterID, &i.Email, &i.Role, &i.TokenHash, &i.Status,
		&i.ExpiresAt, &i.AcceptedAt, &i.CreatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return i, nil
}

func (InvitationsRepo) ListPendingByTenant(ctx context.Context, q Querier, tenantID uuid.UUID) ([]Invitation, error) {
	rows, err := q.Query(ctx, `
		SELECT id, tenant_id, inviter_id, email, role::text, token_hash, status::text,
		       expires_at, accepted_at, created_at
		FROM invitations
		WHERE tenant_id=$1 AND status='pending' AND expires_at > now()
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		var i Invitation
		if err := rows.Scan(&i.ID, &i.TenantID, &i.InviterID, &i.Email, &i.Role, &i.TokenHash,
			&i.Status, &i.ExpiresAt, &i.AcceptedAt, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (InvitationsRepo) MarkAccepted(ctx context.Context, q Querier, tenantID, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx, `
		UPDATE invitations SET status='accepted', accepted_at=$3
		WHERE tenant_id=$1 AND id=$2 AND status='pending'
	`, tenantID, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (InvitationsRepo) Revoke(ctx context.Context, q Querier, tenantID, id uuid.UUID) error {
	tag, err := q.Exec(ctx, `
		UPDATE invitations SET status='revoked'
		WHERE tenant_id=$1 AND id=$2 AND status='pending'
	`, tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
