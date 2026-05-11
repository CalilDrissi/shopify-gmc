package billing

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

	"github.com/example/gmcauditor/internal/mailer"
)

// Dispatcher consumes a parsed Gumroad event and applies it to the database.
// One Dispatcher per server; the HTTP handler wraps each call in a
// transaction so the webhook log row, purchase row, and tenant.plan flip
// commit atomically.
type Dispatcher struct {
	Pool      *pgxpool.Pool
	Catalog   Catalog
	Logger    *slog.Logger
	Mail      mailer.Mailer
	MailFrom  string
	OperatorEmail string // where Rescue confirmations are CC'd
}

// Dispatch applies the event. Caller has already validated the HMAC
// signature and idempotently inserted (or fetched) the gumroad_webhook_events
// row — Dispatch updates it with processed_at on success or error_message on
// failure, all inside the supplied tx.
//
// The tx is committed by the caller, not Dispatch — keeps the HTTP handler
// in charge of transaction lifetime.
func (d *Dispatcher) Dispatch(ctx context.Context, tx pgx.Tx, eventRowID uuid.UUID, e Event) error {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if e.TenantID == uuid.Nil && e.Type != "ping" {
		return d.markErr(ctx, tx, eventRowID, "missing tenant_id custom field")
	}

	prod := d.Catalog.LookupByGumroadID(e.ProductID)
	d.Logger.Info("gumroad_event",
		slog.String("type", e.Type),
		slog.String("sale_id", e.SaleID),
		slog.String("product_id", e.ProductID),
		slog.String("kind", string(prod.Kind)),
		slog.String("tenant_id", e.TenantID.String()),
	)

	var err error
	switch e.Type {
	case "sale":
		err = d.handleSale(ctx, tx, e, prod)
	case "subscription_updated":
		err = d.handleSubscriptionUpdated(ctx, tx, e, prod)
	case "subscription_cancelled":
		err = d.handleSubscriptionCancelled(ctx, tx, e)
	case "subscription_ended":
		err = d.handleSubscriptionEnded(ctx, tx, e)
	case "refund":
		err = d.handleRefund(ctx, tx, e)
	case "ping":
		// Gumroad's "send test ping" — record it processed.
	default:
		// Unknown but signed event — log and mark processed so we don't
		// retry forever.
		d.Logger.Warn("gumroad_unknown_event_type", slog.String("type", e.Type))
	}
	if err != nil {
		return d.markErr(ctx, tx, eventRowID, err.Error())
	}
	_, err = tx.Exec(ctx, `
		UPDATE gumroad_webhook_events
		SET processed_at = now(), tenant_id = $2, product_id = $3, sale_id = $4, signature_ok = true
		WHERE id = $1
	`, eventRowID, nullableUUID(e.TenantID), e.ProductID, e.SaleID)
	return err
}

func (d *Dispatcher) markErr(ctx context.Context, tx pgx.Tx, id uuid.UUID, msg string) error {
	_, _ = tx.Exec(ctx, `
		UPDATE gumroad_webhook_events
		SET error_message = $2, processed_at = now()
		WHERE id = $1
	`, id, msg)
	return errors.New(msg)
}

// ----------------------------------------------------------------------------
// sale (initial purchase + first subscription charge both arrive as "sale")
// ----------------------------------------------------------------------------

func (d *Dispatcher) handleSale(ctx context.Context, tx pgx.Tx, e Event, prod Product) error {
	// Always record the purchase row first.
	plan := prod.PlanTier
	if plan == "" {
		plan = "free" // one-time charges don't change the plan
	}
	if err := d.upsertPurchase(ctx, tx, e, plan); err != nil {
		return err
	}

	switch prod.Kind {
	case KindStarter, KindGrowth, KindAgency:
		// Subscription tier — flip the tenant.
		renews := e.PurchasedAt.AddDate(0, 1, 0)
		if e.EndsAt != nil {
			renews = *e.EndsAt
		}
		_, err := tx.Exec(ctx, `
			UPDATE tenants
			SET plan = $2::plan_tier,
			    plan_renews_at = $3,
			    pending_plan = NULL,
			    pending_plan_at = NULL,
			    gumroad_subscription_id = COALESCE(NULLIF($4,''), gumroad_subscription_id),
			    updated_at = now()
			WHERE id = $1
		`, e.TenantID, prod.PlanTier, renews, e.SubscriptionID)
		return err

	case KindRescue:
		d.notify(ctx, e, "Rescue Audit purchased", fmt.Sprintf(
			"You've purchased a Rescue Audit. We'll be in touch within 24 hours to schedule the deep dive.\n\nReference: %s.", e.SaleID))
		d.notifyOperator(ctx, "Rescue Audit purchase", fmt.Sprintf(
			"tenant=%s sale=%s email=%s", e.TenantID, e.SaleID, e.Email))
		return nil
	}
	return nil
}

// upsertPurchase inserts or updates the purchases row keyed by sale_id.
// Used for sale events; subscription_updated re-runs the upsert when the
// plan changes mid-cycle.
func (d *Dispatcher) upsertPurchase(ctx context.Context, tx pgx.Tx, e Event, plan string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO purchases
		  (tenant_id, gumroad_sale_id, license_key, product_id,
		   plan, amount_cents, currency, status, purchased_at)
		VALUES
		  ($1, $2, NULLIF($3,''), $4,
		   $5::plan_tier, $6, $7, 'active'::purchase_status, $8)
		ON CONFLICT (gumroad_sale_id) DO UPDATE SET
		  plan          = EXCLUDED.plan,
		  amount_cents  = EXCLUDED.amount_cents,
		  currency      = EXCLUDED.currency,
		  status        = 'active'::purchase_status,
		  refunded_at   = NULL,
		  updated_at    = now()
	`, nullableUUID(e.TenantID), e.SaleID, e.LicenseKey, e.ProductID,
		plan, e.PriceCents, e.Currency, e.PurchasedAt)
	return err
}

// ----------------------------------------------------------------------------
// subscription_updated (plan changed mid-cycle, or recurring renewal)
// ----------------------------------------------------------------------------

func (d *Dispatcher) handleSubscriptionUpdated(ctx context.Context, tx pgx.Tx, e Event, prod Product) error {
	// If the new product is one of our subscription tiers, re-set the plan.
	if prod.IsRecurring && prod.PlanTier != "" {
		renews := e.PurchasedAt.AddDate(0, 1, 0)
		if e.EndsAt != nil {
			renews = *e.EndsAt
		}
		_, err := tx.Exec(ctx, `
			UPDATE tenants
			SET plan = $2::plan_tier,
			    plan_renews_at = $3,
			    pending_plan = NULL,
			    pending_plan_at = NULL,
			    updated_at = now()
			WHERE id = $1
		`, e.TenantID, prod.PlanTier, renews)
		if err != nil {
			return err
		}
	}
	if e.SaleID != "" {
		_ = d.upsertPurchase(ctx, tx, e, prod.PlanTier)
	}
	return nil
}

// ----------------------------------------------------------------------------
// subscription_cancelled — keep current plan until plan_renews_at, then
// downgrade to free. We schedule via tenants.pending_plan + pending_plan_at;
// the scheduler reconciles on its tick.
// ----------------------------------------------------------------------------

func (d *Dispatcher) handleSubscriptionCancelled(ctx context.Context, tx pgx.Tx, e Event) error {
	endsAt := time.Now().AddDate(0, 1, 0)
	if e.EndsAt != nil {
		endsAt = *e.EndsAt
	}
	_, err := tx.Exec(ctx, `
		UPDATE tenants
		SET pending_plan = 'free'::plan_tier,
		    pending_plan_at = $2,
		    plan_renews_at = $2,
		    updated_at = now()
		WHERE id = $1
	`, e.TenantID, endsAt)
	return err
}

// ----------------------------------------------------------------------------
// subscription_ended — billing period elapsed, drop to free immediately.
// ----------------------------------------------------------------------------

func (d *Dispatcher) handleSubscriptionEnded(ctx context.Context, tx pgx.Tx, e Event) error {
	_, err := tx.Exec(ctx, `
		UPDATE tenants
		SET plan = 'free'::plan_tier,
		    plan_renews_at = NULL,
		    pending_plan = NULL,
		    pending_plan_at = NULL,
		    updated_at = now()
		WHERE id = $1
	`, e.TenantID)
	return err
}

// ----------------------------------------------------------------------------
// refund — mark the purchase refunded, downgrade to free.
// ----------------------------------------------------------------------------

func (d *Dispatcher) handleRefund(ctx context.Context, tx pgx.Tx, e Event) error {
	if _, err := tx.Exec(ctx, `
		UPDATE purchases
		SET status = 'refunded'::purchase_status,
		    refunded_at = COALESCE($2, now()),
		    updated_at = now()
		WHERE gumroad_sale_id = $1
	`, e.SaleID, e.RefundedAt); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE tenants
		SET plan = 'free'::plan_tier,
		    plan_renews_at = NULL,
		    pending_plan = NULL,
		    pending_plan_at = NULL,
		    updated_at = now()
		WHERE id = $1
	`, e.TenantID)
	return err
}

// ----------------------------------------------------------------------------
// Email notifications. Best-effort: dispatcher errors out only if the DB
// write failed. Failed sends are logged but don't roll back the tx.
// ----------------------------------------------------------------------------

func (d *Dispatcher) notify(ctx context.Context, e Event, subject, body string) {
	if d.Mail == nil || e.Email == "" {
		return
	}
	html := "<p>" + body + "</p>"
	if err := d.Mail.Send(ctx, mailer.Compose(e.Email, d.MailFrom, subject, html)); err != nil {
		d.Logger.Warn("gumroad_notify_send", slog.Any("err", err))
	}
}

func (d *Dispatcher) notifyOperator(ctx context.Context, subject, body string) {
	if d.Mail == nil || d.OperatorEmail == "" {
		return
	}
	html := "<pre>" + body + "</pre>"
	if err := d.Mail.Send(ctx, mailer.Compose(d.OperatorEmail, d.MailFrom, subject, html)); err != nil {
		d.Logger.Warn("gumroad_notify_operator", slog.Any("err", err))
	}
}

func nullableUUID(id uuid.UUID) interface{} {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// MarshalJSON helper — used by the webhook handler to stash the raw payload.
func MarshalForm(form map[string][]string) []byte {
	b, _ := json.Marshal(form)
	return b
}
