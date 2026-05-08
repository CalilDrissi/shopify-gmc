package gmc

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/mailer"
)

// NotifyOwnerOfRevoke emails every owner-role member of the tenant once,
// telling them their GMC connection broke and they need to re-consent.
//
// Used by both the worker (when an audit-time GMC sync hits 401) and the
// scheduler's background refresher. We don't go through the alert
// dispatcher because this isn't a per-audit alert — it's a one-shot
// operational notice with no rate-limiting.
func NotifyOwnerOfRevoke(
	ctx context.Context,
	pool *pgxpool.Pool,
	mail mailer.Mailer,
	baseURL, mailFrom string,
	logger *slog.Logger,
	conn *Connection,
) {
	rows, err := pool.Query(ctx, `
		SELECT u.email, COALESCE(u.name, ''), s.shop_domain, t.slug
		FROM memberships m
		JOIN users   u ON u.id = m.user_id
		JOIN tenants t ON t.id = m.tenant_id
		JOIN stores  s ON s.id = $1
		WHERE m.tenant_id = $2 AND m.role = 'owner'
	`, conn.StoreID, conn.TenantID)
	if err != nil {
		logger.Warn("gmc_revoke_notify_query", slog.Any("err", err))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var email, name, domain, slug string
		if err := rows.Scan(&email, &name, &domain, &slug); err != nil {
			continue
		}
		storeURL := fmt.Sprintf("%s/t/%s/stores/%s", baseURL, slug, conn.StoreID)
		body, err := mailer.RenderGMCRevoked(mailer.GMCRevokedData{
			Name:         name,
			StoreName:    domain,
			MerchantID:   conn.MerchantID,
			ReconnectURL: storeURL,
		})
		if err != nil {
			logger.Warn("gmc_revoke_notify_render", slog.Any("err", err))
			continue
		}
		if err := mail.Send(ctx, mailer.Compose(email, mailFrom,
			"Google Merchant Center disconnected — re-authorize required", body)); err != nil {
			logger.Warn("gmc_revoke_notify_send", slog.Any("err", err))
		}
	}
}
