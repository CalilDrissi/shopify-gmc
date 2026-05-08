package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/audit"
	"github.com/example/gmcauditor/internal/gmc"
	"github.com/example/gmcauditor/internal/mailer"
)

// buildGMCSync returns a GMCSyncFn that the audit pipeline calls between
// Crawl and Detect. Behaviour:
//
//   - returns (nil, nil) when no active connection exists for the store
//     (audit proceeds with crawler-only data)
//   - calls accountstatuses, productstatuses, datafeedstatuses
//   - persists snapshots into gmc_account_snapshots + gmc_product_statuses
//     keyed to the audit_id
//   - on 401 marks the connection revoked + emails the tenant owner
//   - on other errors logs + returns the error (pipeline logs and proceeds)
func buildGMCSync(
	pool *pgxpool.Pool,
	conns *gmc.ConnectionStore,
	gmcBaseURL string,
	mail mailer.Mailer,
	baseURL, mailFrom string,
	logger *slog.Logger,
) audit.GMCSyncFn {
	if conns == nil {
		// No GMC OAuth configured on this worker — disable the stage.
		return nil
	}
	return func(ctx context.Context, in audit.AuditInput) (*audit.GMCContext, error) {
		conn, err := conns.GetByStore(ctx, in.TenantID, in.StoreID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil // no connection, skip
			}
			return nil, fmt.Errorf("gmc: load connection: %w", err)
		}
		if conn.Status != "active" {
			return nil, nil
		}

		// Fresh client per call — bearer access token is per-connection.
		cli := gmc.NewClient(conns.SupplierFor(conn.ID), logger)
		if gmcBaseURL != "" {
			cli.BaseURL = gmcBaseURL
		}

		acct, err := cli.GetAccountStatus(ctx, conn.MerchantID)
		if err != nil {
			handleSyncError(ctx, pool, conns, mail, baseURL, mailFrom, logger, conn, err)
			return nil, err
		}
		prods, err := cli.ListProductStatuses(ctx, conn.MerchantID)
		if err != nil {
			handleSyncError(ctx, pool, conns, mail, baseURL, mailFrom, logger, conn, err)
			return nil, err
		}
		feeds, err := cli.GetDatafeedStatuses(ctx, conn.MerchantID)
		if err != nil {
			handleSyncError(ctx, pool, conns, mail, baseURL, mailFrom, logger, conn, err)
			return nil, err
		}

		// Persist snapshots inside one tx — partial sync data shouldn't be
		// readable by checks if any DB write fails.
		if err := persistSnapshots(ctx, pool, in, conn.ID, acct, prods, feeds); err != nil {
			logger.Warn("gmc_persist_snapshots_failed", slog.Any("err", err))
			// Soft-fail: we still pass the data to checks so the audit
			// reflects the live state even if the snapshot write hiccupped.
		}
		_ = conns.Touch(ctx, conn.ID, "ok", "")
		_ = conns.UpdateAccountSummary(ctx, conn.ID, acct)

		return &audit.GMCContext{
			MerchantID: conn.MerchantID,
			Account:    acct,
			Products:   prods,
			Feeds:      feeds,
		}, nil
	}
}

// handleSyncError centralises the "what does an API failure mean" decision.
// 401 → revoke + email; everything else → just touch the connection with
// the error so the UI can surface it and the operator can investigate.
func handleSyncError(
	ctx context.Context,
	pool *pgxpool.Pool,
	conns *gmc.ConnectionStore,
	mail mailer.Mailer,
	baseURL, mailFrom string,
	logger *slog.Logger,
	conn *gmc.Connection,
	err error,
) {
	if errors.Is(err, gmc.ErrUnauthorized) {
		logger.Warn("gmc_401_revoking", slog.String("connection_id", conn.ID.String()))
		_ = conns.MarkRevoked(ctx, conn.ID, "Google rejected our refresh token (401). Re-consent required.")
		gmc.NotifyOwnerOfRevoke(ctx, pool, mail, baseURL, mailFrom, logger, conn)
		return
	}
	_ = conns.Touch(ctx, conn.ID, "error", err.Error())
}

// persistSnapshots writes the per-audit GMC state. Each call replaces
// product_status rows for this audit (DELETE+INSERT in tx).
func persistSnapshots(
	ctx context.Context,
	pool *pgxpool.Pool,
	in audit.AuditInput,
	connID uuid.UUID,
	acct *gmc.AccountStatus,
	prods []gmc.ProductStatus,
	feeds []gmc.DatafeedStatus,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rawAcct, _ := json.Marshal(acct)
	feedsJSON, _ := json.Marshal(feeds)
	warnings := 0
	susp := 0
	for _, i := range acct.AccountLevelIssues {
		switch i.Severity {
		case "critical":
			susp++
		case "error":
			warnings++
		}
	}
	productCount := acct.Products.Active + acct.Products.Pending + acct.Products.Disapproved + acct.Products.Expiring
	if _, err := tx.Exec(ctx, `
		INSERT INTO gmc_account_snapshots
		  (tenant_id, gmc_connection_id, audit_id, captured_at,
		   product_count, raw_data, account_status, website_claimed,
		   warnings_count, suspensions_count, datafeed_errors)
		VALUES
		  ($1, $2, $3, now(),
		   $4, $5, $6, $7,
		   $8, $9, $10)
	`, in.TenantID, connID, in.AuditID,
		productCount, rawAcct, acct.Status, acct.WebsiteClaimed,
		warnings, susp, feedsJSON); err != nil {
		return fmt.Errorf("snapshot account: %w", err)
	}

	// Replace any prior product-status rows for this audit so re-running is
	// idempotent.
	if _, err := tx.Exec(ctx,
		`DELETE FROM gmc_product_statuses WHERE audit_id = $1`, in.AuditID,
	); err != nil {
		return fmt.Errorf("clear product statuses: %w", err)
	}
	for _, p := range prods {
		issuesJSON, _ := json.Marshal(p.ItemLevelIssues)
		destJSON, _ := json.Marshal(p.DestinationStatuses)
		approval := approvalStatusFor(p)
		if _, err := tx.Exec(ctx, `
			INSERT INTO gmc_product_statuses
			  (tenant_id, store_id, gmc_connection_id, audit_id,
			   product_id, gmc_item_id, approval_status,
			   destination_statuses, issues, last_checked_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
			ON CONFLICT (store_id, product_id) DO UPDATE SET
			  gmc_connection_id    = EXCLUDED.gmc_connection_id,
			  audit_id             = EXCLUDED.audit_id,
			  gmc_item_id          = EXCLUDED.gmc_item_id,
			  approval_status      = EXCLUDED.approval_status,
			  destination_statuses = EXCLUDED.destination_statuses,
			  issues               = EXCLUDED.issues,
			  last_checked_at      = now(),
			  updated_at           = now()
		`, in.TenantID, in.StoreID, connID, in.AuditID,
			p.ProductID, p.ProductID, approval, destJSON, issuesJSON); err != nil {
			return fmt.Errorf("insert product %s: %w", p.ProductID, err)
		}
	}

	return tx.Commit(ctx)
}

func approvalStatusFor(p gmc.ProductStatus) string {
	// Aggregate per-destination statuses into one label.
	pending := false
	for _, d := range p.DestinationStatuses {
		if d.Status == "approved" {
			return "approved"
		}
		if d.Status == "pending" {
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	if len(p.ItemLevelIssues) > 0 {
		return "disapproved"
	}
	return "unknown"
}

var _ = time.Hour // keep time import if optimised away