package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/example/gmcauditor/internal/billing"
)

// GumroadWebhook is the public ingress for Gumroad pings.
//
// Lifecycle (all in one DB tx):
//  1. Read the raw body (we need the bytes for HMAC verification).
//  2. Verify X-Gumroad-Signature against GUMROAD_WEBHOOK_SECRET.
//     Failure → 401, no DB write. (Gumroad will retry with backoff.)
//  3. Parse the form; extract event type, sale_id, custom fields.
//  4. INSERT INTO gumroad_webhook_events with ON CONFLICT DO NOTHING on
//     (event_type, sale_id) — the dedup key. If we got a conflict the
//     event was already processed; return 200 immediately so Gumroad
//     stops retrying.
//  5. Dispatch — update tenant plan, insert/update purchase, etc.
//  6. UPDATE the event row with processed_at + signature_ok.
//  7. Commit + return 200.
func (h *Handlers) GumroadWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
	if err != nil {
		http.Error(w, "request too large", http.StatusBadRequest)
		return
	}
	signature := r.Header.Get("X-Gumroad-Signature")
	if !billing.VerifySignature(h.GumroadSecret, body, signature) {
		h.Logger.Warn("gumroad_bad_signature",
			slog.String("remote", r.RemoteAddr),
			slog.Int("body_bytes", len(body)),
		)
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}

	form, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	event := billing.ParseForm(form)
	// payload is jsonb — store the parsed form as a JSON object so we
	// can introspect later with `->`.
	payloadJSON := billing.MarshalForm(form)

	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		h.Logger.Error("gumroad_tx_begin", slog.Any("err", err))
		http.Error(w, "tx begin failed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(r.Context())

	// Insert the webhook event row. Dedup by (event_type, sale_id).
	var rowID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		INSERT INTO gumroad_webhook_events
			(event_type, sale_id, payload, signature_ok)
		VALUES
			($1, NULLIF($2,''), $3, true)
		ON CONFLICT (event_type, sale_id) WHERE sale_id IS NOT NULL DO NOTHING
		RETURNING id
	`, event.Type, event.SaleID, payloadJSON).Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already processed — Gumroad retried.
		h.Logger.Info("gumroad_duplicate_skipped",
			slog.String("type", event.Type),
			slog.String("sale_id", event.SaleID))
		_ = tx.Commit(r.Context())
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		h.Logger.Error("gumroad_event_insert", slog.Any("err", err))
		http.Error(w, "insert failed", http.StatusInternalServerError)
		return
	}

	if err := h.Gumroad.Dispatch(r.Context(), tx, rowID, event); err != nil {
		// dispatcher already wrote error_message into the event row. Commit
		// so we keep the row for inspection; return 200 so Gumroad doesn't
		// retry — there's nothing it could do about a dispatch failure.
		_ = tx.Commit(r.Context())
		h.Logger.Warn("gumroad_dispatch_failed", slog.Any("err", err))
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		h.Logger.Error("gumroad_tx_commit", slog.Any("err", err))
		http.Error(w, "commit failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ============================================================================
// Pricing + Billing pages
// ============================================================================

// PricingPage renders the public pricing page with overlay buttons. We
// supply tenant_id when the visitor is signed in so checkout maps the sale
// to the right workspace; anonymous visitors get a "sign up to upgrade"
// CTA instead.
func (h *Handlers) PricingPage(w http.ResponseWriter, r *http.Request) {
	cat := h.GumroadCatalog
	var tenantID string
	if u, ok := userFromCookie(r, h); ok {
		// Find the user's first tenant (usually the only one) so the overlay
		// pre-fills tenant_id. Owner-only flow happens server-side via the
		// webhook's tenant lookup, not via the URL.
		tenantID = h.firstTenantIDFor(r.Context(), u.ID)
	}
	h.render(w, r, "pricing", map[string]any{
		"Title":    "Pricing",
		"Tiers":    cat.SubscriptionTiers(),
		"Rescue":   cat.ByKind[billing.KindRescue],
		"DFY":      cat.ByKind[billing.KindDFY],
		"TenantID": tenantID,
	})
}

// BillingPage is the per-tenant billing dashboard.
func (h *Handlers) BillingPage(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	d.Title = "Billing"

	// Recent purchases.
	rows, err := h.Pool.Query(r.Context(), `
		SELECT id, COALESCE(gumroad_sale_id,''), COALESCE(product_id,''), plan::text,
		       COALESCE(amount_cents,0), COALESCE(currency,''),
		       status::text, purchased_at, refunded_at
		FROM purchases
		WHERE tenant_id = $1
		ORDER BY COALESCE(purchased_at, created_at) DESC
		LIMIT 50
	`, d.Tenant.ID)
	type purchaseRow struct {
		ID          uuid.UUID
		SaleID      string
		ProductID   string
		Plan        string
		AmountCents int
		Currency    string
		Status      string
		PurchasedAt *time.Time
		RefundedAt  *time.Time
	}
	var purchases []purchaseRow
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p purchaseRow
			if err := rows.Scan(&p.ID, &p.SaleID, &p.ProductID, &p.Plan,
				&p.AmountCents, &p.Currency, &p.Status, &p.PurchasedAt, &p.RefundedAt); err != nil {
				continue
			}
			purchases = append(purchases, p)
		}
	}

	// Pending downgrade?
	var pendingPlan, gumroadSubID *string
	var pendingAt *time.Time
	_ = h.Pool.QueryRow(r.Context(), `
		SELECT pending_plan::text, pending_plan_at, gumroad_subscription_id
		FROM tenants WHERE id = $1
	`, d.Tenant.ID).Scan(&pendingPlan, &pendingAt, &gumroadSubID)

	d.Data = map[string]any{
		"Tiers":         h.GumroadCatalog.SubscriptionTiers(),
		"Rescue":        h.GumroadCatalog.ByKind[billing.KindRescue],
		"DFY":           h.GumroadCatalog.ByKind[billing.KindDFY],
		"Purchases":     purchases,
		"PendingPlan":   pendingPlan,
		"PendingAt":     pendingAt,
		"GumroadSubID":  gumroadSubID,
		"ReturnGumroad": r.URL.Query().Get("gumroad_return") == "1",
	}
	h.renderTenant(w, r, "billing", d)
}

// BillingPollFragment is HTMX-polled by the "Processing payment" page after
// a Gumroad return — it returns a small JSON-ish HTML snippet indicating
// whether we've seen the webhook for the most-recent purchase yet.
//
// 200 with `<div data-state="ready">` once the plan flipped or a purchase
// landed in the last 60 seconds. Otherwise the same div with `data-state="pending"`
// and HX-Trigger "billing-poll" to keep the loop alive.
func (h *Handlers) BillingPollFragment(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	var lastPurchaseAt *time.Time
	_ = h.Pool.QueryRow(r.Context(), `
		SELECT MAX(COALESCE(purchased_at, created_at)) FROM purchases WHERE tenant_id=$1
	`, d.Tenant.ID).Scan(&lastPurchaseAt)
	state := "pending"
	if lastPurchaseAt != nil && time.Since(*lastPurchaseAt) < time.Minute {
		state = "ready"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if state == "pending" {
		fmt.Fprintf(w, `<div id="billing-poll" hx-get="/t/%s/billing/poll" hx-trigger="every 2s" hx-swap="outerHTML" data-state="pending">
		  <p>Processing payment… (we're waiting for Gumroad's webhook to land)</p>
		</div>`, d.Tenant.Slug)
		return
	}
	fmt.Fprintf(w, `<div id="billing-poll" data-state="ready">
		<p>Payment processed. <a href="/t/%s/billing">Reload your billing page →</a></p>
	</div>`, d.Tenant.Slug)
}

// ============================================================================
// helpers
// ============================================================================

// userFromCookie reads the session cookie and returns a minimal *userView
// (just ID needed) so PricingPage can pre-fill tenant_id when the visitor
// is signed in. Returns ok=false otherwise.
func userFromCookie(r *http.Request, h *Handlers) (*userView, bool) {
	cv, err := h.Cookies.Read(r, sessionCookieName)
	if err != nil {
		return nil, false
	}
	sess, err := h.Sessions.Get(r.Context(), cv.Token)
	if err != nil {
		return nil, false
	}
	return &userView{ID: sess.UserID}, true
}

func (h *Handlers) firstTenantIDFor(ctx context.Context, userID uuid.UUID) string {
	var id uuid.UUID
	_ = h.Pool.QueryRow(ctx, `
		SELECT t.id FROM tenants t
		JOIN memberships m ON m.tenant_id = t.id
		WHERE m.user_id = $1
		ORDER BY m.created_at LIMIT 1
	`, userID).Scan(&id)
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
