package gmc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/settings"
)

// ConnectionStore mediates between persistence and OAuth/HTTP layers.
//
// It owns one privileged thing: the AES-256-GCM cipher that wraps the
// refresh token. Refresh tokens are decrypted only into a local var inside
// SupplierFor, used to mint a short-lived access token, and discarded; we
// never persist the decrypted form anywhere on disk or in logs.
type ConnectionStore struct {
	Pool   *pgxpool.Pool
	Cipher *settings.Cipher
	OAuth  *OAuth

	// access-token in-memory cache, keyed by connection ID.
	mu sync.Mutex
	tok map[uuid.UUID]cachedTok
}

type cachedTok struct {
	access string
	exp    time.Time
}

func NewConnectionStore(pool *pgxpool.Pool, cipher *settings.Cipher, oauth *OAuth) *ConnectionStore {
	return &ConnectionStore{Pool: pool, Cipher: cipher, OAuth: oauth, tok: map[uuid.UUID]cachedTok{}}
}

// Connection is the loaded form of a store_gmc_connections row, minus
// the encrypted blob.
type Connection struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	StoreID       uuid.UUID
	MerchantID    string
	AccountEmail  string
	Scope         string
	Status        string
	LastSyncAt    *time.Time
	LastSyncStatus *string
	RevokedAt     *time.Time
	AccountStatus *string
	Warnings      int
	Suspensions   int
	WebsiteClaimed *bool
}

// Insert encrypts the refresh token and writes a new connection. If a
// connection for (store_id, merchant_id) already exists, the row is updated
// in place — re-consenting with the same merchant overwrites the encrypted
// token, account_email, and scope.
func (s *ConnectionStore) Upsert(
	ctx context.Context,
	tenantID, storeID uuid.UUID,
	merchantID, accountEmail string,
	tok *Token,
) (uuid.UUID, error) {
	if tok.RefreshToken == "" {
		return uuid.Nil, errors.New("gmc: refresh token missing — was prompt=consent set?")
	}
	enc, err := s.Cipher.Encrypt([]byte(tok.RefreshToken))
	if err != nil {
		return uuid.Nil, fmt.Errorf("gmc: encrypt refresh token: %w", err)
	}
	id := uuid.New()
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO store_gmc_connections
			(id, tenant_id, store_id, merchant_id, account_email,
			 refresh_token_encrypted, token_nonce, token_expires_at,
			 status, scope, created_at, updated_at)
		VALUES
			($1, $2, $3, $4, NULLIF($5,''),
			 $6, ''::bytea, $7,
			 'active'::gmc_connection_status, $8, now(), now())
		ON CONFLICT (store_id, merchant_id) DO UPDATE SET
			refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
			token_expires_at        = EXCLUDED.token_expires_at,
			account_email           = EXCLUDED.account_email,
			status                  = 'active'::gmc_connection_status,
			revoked_at              = NULL,
			scope                   = EXCLUDED.scope,
			updated_at              = now()
		RETURNING id
	`, id, tenantID, storeID, merchantID, accountEmail, enc, tok.ExpiresAt, tok.Scope).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("gmc: upsert connection: %w", err)
	}
	return id, nil
}

// GetByStore loads the active connection for a store. Returns pgx.ErrNoRows
// when the store isn't connected.
func (s *ConnectionStore) GetByStore(ctx context.Context, tenantID, storeID uuid.UUID) (*Connection, error) {
	var c Connection
	err := s.Pool.QueryRow(ctx, `
		SELECT id, tenant_id, store_id, merchant_id, COALESCE(account_email::text,''),
		       COALESCE(scope,''), status::text,
		       last_sync_at, last_sync_status, revoked_at,
		       account_status, warnings_count, suspensions_count, website_claimed
		FROM store_gmc_connections
		WHERE tenant_id = $1 AND store_id = $2 AND status <> 'revoked'
		ORDER BY created_at DESC LIMIT 1
	`, tenantID, storeID).Scan(
		&c.ID, &c.TenantID, &c.StoreID, &c.MerchantID, &c.AccountEmail,
		&c.Scope, &c.Status,
		&c.LastSyncAt, &c.LastSyncStatus, &c.RevokedAt,
		&c.AccountStatus, &c.Warnings, &c.Suspensions, &c.WebsiteClaimed,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MarkRevoked flips the row to revoked and clears the encrypted token. Used
// from the disconnect handler and from Client error handlers when Google
// returns 401.
func (s *ConnectionStore) MarkRevoked(ctx context.Context, connID uuid.UUID, reason string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE store_gmc_connections
		SET status = 'revoked'::gmc_connection_status,
		    revoked_at = now(),
		    refresh_token_encrypted = NULL,
		    last_error_message = NULLIF($2,''),
		    updated_at = now()
		WHERE id = $1
	`, connID, reason)
	s.mu.Lock()
	delete(s.tok, connID)
	s.mu.Unlock()
	return err
}

// SupplierFor returns a TokenSupplier closure that knows how to refresh
// tokens for one connection. The closure decrypts the refresh token only
// for the duration of the OAuth refresh call; the access token is cached
// in-memory for its lifetime (or 5 minutes, whichever is shorter).
func (s *ConnectionStore) SupplierFor(connID uuid.UUID) TokenSupplier {
	return func(ctx context.Context) (string, error) {
		s.mu.Lock()
		c := s.tok[connID]
		s.mu.Unlock()
		if c.access != "" && time.Until(c.exp) > 30*time.Second {
			return c.access, nil
		}
		// Decrypt only here, only into a local var.
		var enc []byte
		if err := s.Pool.QueryRow(ctx,
			`SELECT refresh_token_encrypted FROM store_gmc_connections WHERE id = $1 AND status='active'`,
			connID,
		).Scan(&enc); err != nil {
			return "", fmt.Errorf("gmc: load refresh token: %w", err)
		}
		if len(enc) == 0 {
			return "", errors.New("gmc: connection has no refresh token")
		}
		plain, err := s.Cipher.Decrypt(enc)
		if err != nil {
			return "", fmt.Errorf("gmc: decrypt refresh token: %w", err)
		}
		tok, err := s.OAuth.Refresh(ctx, string(plain))
		// Best-effort: zero out the plaintext slice immediately.
		for i := range plain {
			plain[i] = 0
		}
		if err != nil {
			return "", err
		}
		exp := tok.ExpiresAt
		if cap := time.Now().Add(5 * time.Minute); cap.Before(exp) {
			exp = cap
		}
		s.mu.Lock()
		s.tok[connID] = cachedTok{access: tok.AccessToken, exp: exp}
		s.mu.Unlock()
		return tok.AccessToken, nil
	}
}

// LoadFreshAccessToken is a one-shot helper used at OAuth-callback time
// before we know the connection ID. It just refreshes once and returns
// the access token + expiry.
func (s *ConnectionStore) LoadFreshAccessToken(ctx context.Context, refreshToken string) (string, time.Time, error) {
	tok, err := s.OAuth.Refresh(ctx, refreshToken)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok.AccessToken, tok.ExpiresAt, nil
}

// Touch records a sync attempt's outcome. status: "ok" or "error".
func (s *ConnectionStore) Touch(ctx context.Context, connID uuid.UUID, status, errMsg string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE store_gmc_connections
		SET last_sync_at        = now(),
		    last_sync_status    = $2,
		    last_error_message  = NULLIF($3,''),
		    updated_at          = now()
		WHERE id = $1
	`, connID, status, errMsg)
	return err
}

// UpdateAccountSummary refreshes the cached health flags from a fresh
// account-status fetch. Called after each successful sync.
func (s *ConnectionStore) UpdateAccountSummary(ctx context.Context, connID uuid.UUID, st *AccountStatus) error {
	warnings := 0
	susp := 0
	for _, i := range st.AccountLevelIssues {
		switch i.Severity {
		case "critical":
			susp++
		case "error":
			warnings++
		}
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE store_gmc_connections
		SET account_status      = $2,
		    warnings_count      = $3,
		    suspensions_count   = $4,
		    website_claimed     = $5,
		    updated_at          = now()
		WHERE id = $1
	`, connID, st.Status, warnings, susp, st.WebsiteClaimed)
	return err
}

// ----------------------------------------------------------------------------
// Sentinel for "not connected" — saves callers from importing pgx in handlers
// just to compare ErrNoRows.
// ----------------------------------------------------------------------------

// ErrNotConnected is returned by GetByStore when no active connection exists.
var ErrNotConnected = pgx.ErrNoRows

var _ = ErrNotConnected
