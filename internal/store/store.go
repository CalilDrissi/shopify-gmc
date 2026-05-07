package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("store: not found")

type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct {
	pool *pgxpool.Pool

	Users                    *UsersRepo
	Sessions                 *SessionsRepo
	EmailVerificationTokens  *EmailVerificationTokensRepo
	PasswordResetTokens      *PasswordResetTokensRepo
	Tenants                  *TenantsRepo
	Memberships              *MembershipsRepo
	Invitations              *InvitationsRepo
	UsageCounters            *UsageCountersRepo
	Stores                   *StoresRepo
	StoreAlertSubscriptions  *StoreAlertSubscriptionsRepo
	StoreGmcConnections      *StoreGmcConnectionsRepo
	GmcAccountSnapshots      *GmcAccountSnapshotsRepo
	GmcProductStatuses       *GmcProductStatusesRepo
	Audits                   *AuditsRepo
	Issues                   *IssuesRepo
	AuditDiffs               *AuditDiffsRepo
	AuditJobs                *AuditJobsRepo
	Purchases                *PurchasesRepo
	GumroadWebhookEvents     *GumroadWebhookEventsRepo
	PlatformAdmins           *PlatformAdminsRepo
	PlatformAdminAuditLog    *PlatformAdminAuditLogRepo
	ImpersonationLog         *ImpersonationLogRepo
	PlatformSettings         *PlatformSettingsRepo
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:                    pool,
		Users:                   &UsersRepo{},
		Sessions:                &SessionsRepo{},
		EmailVerificationTokens: &EmailVerificationTokensRepo{},
		PasswordResetTokens:     &PasswordResetTokensRepo{},
		Tenants:                 &TenantsRepo{},
		Memberships:             &MembershipsRepo{},
		Invitations:             &InvitationsRepo{},
		UsageCounters:           &UsageCountersRepo{},
		Stores:                  &StoresRepo{},
		StoreAlertSubscriptions: &StoreAlertSubscriptionsRepo{},
		StoreGmcConnections:     &StoreGmcConnectionsRepo{},
		GmcAccountSnapshots:     &GmcAccountSnapshotsRepo{},
		GmcProductStatuses:      &GmcProductStatusesRepo{},
		Audits:                  &AuditsRepo{},
		Issues:                  &IssuesRepo{},
		AuditDiffs:              &AuditDiffsRepo{},
		AuditJobs:               &AuditJobsRepo{},
		Purchases:               &PurchasesRepo{},
		GumroadWebhookEvents:    &GumroadWebhookEventsRepo{},
		PlatformAdmins:          &PlatformAdminsRepo{},
		PlatformAdminAuditLog:   &PlatformAdminAuditLogRepo{},
		ImpersonationLog:        &ImpersonationLogRepo{},
		PlatformSettings:        &PlatformSettingsRepo{},
	}
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) Querier() Querier { return s.pool }

// RequestContext binds a tenant and user to the current transaction via SET LOCAL,
// driving both the WHERE clauses in repos AND the RLS policies as defense in depth.
type RequestContext struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// SetRequestContext sets app.current_tenant_id and app.current_user_id at the
// transaction level using set_config(name, value, is_local=true). Caller must
// already be inside a transaction.
func SetRequestContext(ctx context.Context, q Querier, rc RequestContext) error {
	if rc.TenantID != uuid.Nil {
		if _, err := q.Exec(ctx, `SELECT set_config('app.current_tenant_id', $1, true)`, rc.TenantID.String()); err != nil {
			return fmt.Errorf("set tenant id: %w", err)
		}
	}
	if rc.UserID != uuid.Nil {
		if _, err := q.Exec(ctx, `SELECT set_config('app.current_user_id', $1, true)`, rc.UserID.String()); err != nil {
			return fmt.Errorf("set user id: %w", err)
		}
	}
	return nil
}

// WithRequestContext begins a transaction, applies SET LOCAL, runs fn, and
// commits unless fn returns an error.
func (s *Store) WithRequestContext(ctx context.Context, rc RequestContext, fn func(q Querier) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := SetRequestContext(ctx, tx, rc); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func translatePgErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
