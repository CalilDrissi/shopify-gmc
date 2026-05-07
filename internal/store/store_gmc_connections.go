package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type StoreGmcConnection struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	StoreID               uuid.UUID
	MerchantID            string
	AccountEmail          *string
	AccessTokenEncrypted  []byte
	RefreshTokenEncrypted []byte
	TokenNonce            []byte
	TokenExpiresAt        *time.Time
	Status                string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type StoreGmcConnectionsRepo struct{}

func (StoreGmcConnectionsRepo) Insert(ctx context.Context, q Querier, tenantID uuid.UUID, c *StoreGmcConnection) error {
	c.TenantID = tenantID
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO store_gmc_connections
		  (tenant_id, store_id, merchant_id, account_email,
		   access_token_encrypted, refresh_token_encrypted, token_nonce, token_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, status::text, created_at, updated_at
	`, tenantID, c.StoreID, c.MerchantID, c.AccountEmail,
		c.AccessTokenEncrypted, c.RefreshTokenEncrypted, c.TokenNonce, c.TokenExpiresAt,
	).Scan(&c.ID, &c.Status, &c.CreatedAt, &c.UpdatedAt))
}

func (StoreGmcConnectionsRepo) GetByStore(ctx context.Context, q Querier, tenantID, storeID uuid.UUID) (*StoreGmcConnection, error) {
	c := &StoreGmcConnection{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, store_id, merchant_id, account_email,
		       access_token_encrypted, refresh_token_encrypted, token_nonce, token_expires_at,
		       status::text, created_at, updated_at
		FROM store_gmc_connections
		WHERE tenant_id=$1 AND store_id=$2
	`, tenantID, storeID).Scan(&c.ID, &c.TenantID, &c.StoreID, &c.MerchantID, &c.AccountEmail,
		&c.AccessTokenEncrypted, &c.RefreshTokenEncrypted, &c.TokenNonce, &c.TokenExpiresAt,
		&c.Status, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return c, nil
}

func (StoreGmcConnectionsRepo) UpdateTokens(ctx context.Context, q Querier, tenantID, id uuid.UUID,
	access, refresh, nonce []byte, expiresAt *time.Time) error {
	tag, err := q.Exec(ctx, `
		UPDATE store_gmc_connections
		SET access_token_encrypted=$3, refresh_token_encrypted=$4, token_nonce=$5,
		    token_expires_at=$6, status='active', updated_at=now()
		WHERE tenant_id=$1 AND id=$2
	`, tenantID, id, access, refresh, nonce, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (StoreGmcConnectionsRepo) MarkRevoked(ctx context.Context, q Querier, tenantID, id uuid.UUID) error {
	tag, err := q.Exec(ctx,
		`UPDATE store_gmc_connections SET status='revoked', updated_at=now() WHERE tenant_id=$1 AND id=$2`,
		tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
