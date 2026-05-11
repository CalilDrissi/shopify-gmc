// Integration tests for the full Gumroad webhook HTTP path: HMAC
// verification → INSERT idempotently into gumroad_webhook_events →
// dispatch → response. Requires DATABASE_URL; the tests skip cleanly
// without a Postgres available.
package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/billing"
)

func requirePoolWeb(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("postgres unreachable; skipping: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("postgres ping failed; skipping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedWebTenant(t *testing.T, pool *pgxpool.Pool, plan string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	slug := "webhook-test-" + id.String()[:8]
	_, err := pool.Exec(context.Background(), `
		INSERT INTO tenants (id, name, slug, kind, plan, created_at, updated_at)
		VALUES ($1, 'Webhook Test', $2, 'individual'::tenant_kind, $3::plan_tier, now(), now())
	`, id, slug, plan)
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

func newGumroadHandler(pool *pgxpool.Pool, secret []byte) *Handlers {
	return &Handlers{
		Pool:           pool,
		Logger:         slog.Default(),
		GumroadSecret:  secret,
		GumroadCatalog: billing.LoadCatalog(),
		Gumroad: &billing.Dispatcher{
			Pool:    pool,
			Catalog: billing.LoadCatalog(),
			Logger:  slog.Default(),
		},
	}
}

func postSigned(t *testing.T, h *Handlers, secret []byte, body string, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gumroad", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sig == "" && len(secret) > 0 {
		sig = billing.SignBody(secret, []byte(body))
	}
	if sig != "" {
		req.Header.Set("X-Gumroad-Signature", sig)
	}
	rec := httptest.NewRecorder()
	h.GumroadWebhook(rec, req)
	return rec
}

// TestGumroadWebhook_RejectsBadSignature ensures a payload with a bogus
// HMAC never reaches the dispatcher and never writes to the events table.
func TestGumroadWebhook_RejectsBadSignature(t *testing.T) {
	pool := requirePoolWeb(t)
	secret := []byte("test-secret-rejects-bad-sig")
	h := newGumroadHandler(pool, secret)

	body := "resource_name=ping"

	// Wrong-but-valid-hex signature.
	rec := postSigned(t, h, secret, body, "deadbeef0000000000000000")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad-hex sig status=%d want 401", rec.Code)
	}

	// Missing X-Gumroad-Signature header entirely. Build the request
	// directly so the helper's auto-sign behaviour can't sneak in.
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gumroad", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.GumroadWebhook(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing-signature status=%d want 401", rec.Code)
	}
}

// TestGumroadWebhook_SignedSaleFlipsPlan exercises the full handler path:
// signed sale payload → 200 → tenant.plan flipped → events row marked
// processed.
func TestGumroadWebhook_SignedSaleFlipsPlan(t *testing.T) {
	t.Setenv("GUMROAD_PRODUCT_STARTER", "gmc-starter")
	pool := requirePoolWeb(t)
	tenantID := seedWebTenant(t, pool, "free")
	secret := []byte("test-secret-signed-sale")
	h := newGumroadHandler(pool, secret)

	saleID := "websale-" + uuid.NewString()
	form := url.Values{}
	form.Set("sale_id", saleID)
	form.Set("product_permalink", "gmc-starter")
	form.Set("price_cents", "1200")
	form.Set("recurrence", "monthly")
	form.Set("tenant_id", tenantID.String())
	body := form.Encode()

	rec := postSigned(t, h, secret, body, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}

	var plan, eventProcessed string
	_ = pool.QueryRow(context.Background(), `SELECT plan::text FROM tenants WHERE id=$1`, tenantID).Scan(&plan)
	if plan != "pro" {
		t.Errorf("plan=%q after signed sale; want pro", plan)
	}
	_ = pool.QueryRow(context.Background(), `
		SELECT CASE WHEN processed_at IS NULL THEN 'no' ELSE 'yes' END
		FROM gumroad_webhook_events WHERE sale_id=$1
	`, saleID).Scan(&eventProcessed)
	if eventProcessed != "yes" {
		t.Errorf("event processed=%q; want yes", eventProcessed)
	}
}

// TestGumroadWebhook_DuplicateReturnsOk verifies Gumroad's retries of an
// already-processed (event_type, sale_id) come back as 200 without
// double-dispatching (no second purchase row, no plan re-flip side
// effects).
func TestGumroadWebhook_DuplicateReturnsOk(t *testing.T) {
	t.Setenv("GUMROAD_PRODUCT_STARTER", "gmc-starter")
	pool := requirePoolWeb(t)
	tenantID := seedWebTenant(t, pool, "free")
	secret := []byte("test-secret-duplicate")
	h := newGumroadHandler(pool, secret)

	saleID := "dup-" + uuid.NewString()
	form := url.Values{}
	form.Set("sale_id", saleID)
	form.Set("product_permalink", "gmc-starter")
	form.Set("price_cents", "1200")
	form.Set("recurrence", "monthly")
	form.Set("tenant_id", tenantID.String())
	body := form.Encode()

	// First delivery.
	rec1 := postSigned(t, h, secret, body, "")
	if rec1.Code != http.StatusOK {
		t.Fatalf("first delivery status=%d want 200", rec1.Code)
	}
	// Replay (same body, same signature).
	rec2 := postSigned(t, h, secret, body, "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay status=%d want 200 (dedup, not error)", rec2.Code)
	}

	// Exactly one events row + one purchases row should exist for this sale.
	var events, purchases int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM gumroad_webhook_events WHERE sale_id=$1`, saleID).Scan(&events)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM purchases WHERE gumroad_sale_id=$1`, saleID).Scan(&purchases)
	if events != 1 {
		t.Errorf("events=%d; want 1 (dedup must hold)", events)
	}
	if purchases != 1 {
		t.Errorf("purchases=%d; want 1 (no second insert on replay)", purchases)
	}
}
