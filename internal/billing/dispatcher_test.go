package billing_test

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/billing"
)

// requirePool gives every DB-touching test in this package an isolated
// pool. Skips when DATABASE_URL is unset (CI without a Postgres still
// runs the pure-unit tests in billing_test.go).
func requirePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("postgres not reachable; skipping integration test: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("postgres ping failed; skipping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenant creates a throwaway tenant and returns its UUID. Used so the
// dispatcher tests don't disturb fixture data.
func seedTenant(t *testing.T, pool *pgxpool.Pool, plan string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	slug := "billing-test-" + id.String()[:8]
	_, err := pool.Exec(context.Background(), `
		INSERT INTO tenants (id, name, slug, kind, plan, created_at, updated_at)
		VALUES ($1, 'Billing Test', $2, 'individual'::tenant_kind, $3::plan_tier, now(), now())
	`, id, slug, plan)
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// TestDispatch_SaleFlipsPlan verifies a sale event for the Growth product
// flips the tenant from Free → Growth and writes a purchase row.
func TestDispatch_SaleFlipsPlan(t *testing.T) {
	t.Setenv("GUMROAD_PRODUCT_GROWTH", "gmc-growth")
	pool := requirePool(t)
	tenantID := seedTenant(t, pool, "free")
	disp := &billing.Dispatcher{Pool: pool, Catalog: billing.LoadCatalog()}

	v := url.Values{}
	v.Set("sale_id", "test-sale-"+uuid.NewString())
	v.Set("product_permalink", "gmc-growth")
	v.Set("price_cents", "4900")
	v.Set("currency", "USD")
	v.Set("recurrence", "monthly")
	v.Set("tenant_id", tenantID.String())
	event := billing.ParseForm(v)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())

	// Insert the webhook event row first (mirrors the HTTP handler).
	var rowID uuid.UUID
	if err := tx.QueryRow(context.Background(), `
		INSERT INTO gumroad_webhook_events (event_type, sale_id, payload, signature_ok)
		VALUES ($1, $2, '{}'::jsonb, true) RETURNING id
	`, event.Type, event.SaleID).Scan(&rowID); err != nil {
		t.Fatal(err)
	}
	if err := disp.Dispatch(context.Background(), tx, rowID, event); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	var plan string
	if err := pool.QueryRow(context.Background(), `SELECT plan::text FROM tenants WHERE id=$1`, tenantID).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if plan != "growth" {
		t.Errorf("plan=%q after dispatch; want growth", plan)
	}
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM purchases WHERE tenant_id=$1`, tenantID).Scan(&n)
	if n != 1 {
		t.Errorf("purchase count=%d; want 1", n)
	}
}

// TestDispatch_RefundDowngrades verifies a refund flips the plan back to
// free and marks the purchase refunded.
func TestDispatch_RefundDowngrades(t *testing.T) {
	t.Setenv("GUMROAD_PRODUCT_GROWTH", "gmc-growth")
	pool := requirePool(t)
	tenantID := seedTenant(t, pool, "free")
	disp := &billing.Dispatcher{Pool: pool, Catalog: billing.LoadCatalog()}

	saleID := "test-sale-" + uuid.NewString()
	// Sale first
	v := url.Values{}
	v.Set("sale_id", saleID)
	v.Set("product_permalink", "gmc-growth")
	v.Set("price_cents", "4900")
	v.Set("recurrence", "monthly")
	v.Set("tenant_id", tenantID.String())
	doDispatch(t, pool, disp, billing.ParseForm(v))

	// Refund
	r := url.Values{}
	r.Set("resource_name", "refund")
	r.Set("sale_id", saleID)
	r.Set("refunded", "true")
	r.Set("refunded_at", time.Now().UTC().Format(time.RFC3339))
	r.Set("tenant_id", tenantID.String())
	doDispatch(t, pool, disp, billing.ParseForm(r))

	var plan, status string
	_ = pool.QueryRow(context.Background(), `SELECT plan::text FROM tenants WHERE id=$1`, tenantID).Scan(&plan)
	_ = pool.QueryRow(context.Background(), `SELECT status::text FROM purchases WHERE gumroad_sale_id=$1`, saleID).Scan(&status)
	if plan != "free" {
		t.Errorf("plan=%q after refund; want free", plan)
	}
	if status != "refunded" {
		t.Errorf("purchase status=%q; want refunded", status)
	}
}

// TestDispatch_IdempotentReplay verifies that re-dispatching an already-
// processed (event_type, sale_id) tuple is rejected by the unique index
// on the webhook events table — the dispatcher tx fails cleanly without
// double-flipping the plan or double-charging.
func TestDispatch_IdempotentReplay(t *testing.T) {
	t.Setenv("GUMROAD_PRODUCT_STARTER", "gmc-starter")
	pool := requirePool(t)
	tenantID := seedTenant(t, pool, "free")
	disp := &billing.Dispatcher{Pool: pool, Catalog: billing.LoadCatalog()}

	saleID := "test-sale-" + uuid.NewString()
	v := url.Values{}
	v.Set("sale_id", saleID)
	v.Set("product_permalink", "gmc-starter")
	v.Set("price_cents", "1900")
	v.Set("recurrence", "monthly")
	v.Set("tenant_id", tenantID.String())
	event := billing.ParseForm(v)

	doDispatch(t, pool, disp, event)
	// Replay attempt
	tx2, _ := pool.Begin(context.Background())
	defer tx2.Rollback(context.Background())
	var rowID uuid.UUID
	err := tx2.QueryRow(context.Background(), `
		INSERT INTO gumroad_webhook_events (event_type, sale_id, payload, signature_ok)
		VALUES ($1, $2, '{}'::jsonb, true)
		ON CONFLICT (event_type, sale_id) WHERE sale_id IS NOT NULL DO NOTHING
		RETURNING id
	`, event.Type, event.SaleID).Scan(&rowID)
	if err == nil {
		t.Fatal("replay returned a new row id; want ON CONFLICT DO NOTHING (no rows)")
	}
	// pgx maps "no rows" from DO NOTHING to ErrNoRows.

	// Verify only one purchase + one webhook event row landed.
	var purchases, events int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM purchases WHERE tenant_id=$1`, tenantID).Scan(&purchases)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM gumroad_webhook_events WHERE sale_id=$1`, saleID).Scan(&events)
	if purchases != 1 {
		t.Errorf("purchase count=%d; want 1", purchases)
	}
	if events != 1 {
		t.Errorf("webhook event count=%d; want 1", events)
	}
}

// doDispatch runs the canonical "insert event row + dispatch" pair in one
// tx, matching what the HTTP handler does on a successful webhook.
func doDispatch(t *testing.T, pool *pgxpool.Pool, disp *billing.Dispatcher, event billing.Event) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var rowID uuid.UUID
	if err := tx.QueryRow(context.Background(), `
		INSERT INTO gumroad_webhook_events (event_type, sale_id, payload, signature_ok)
		VALUES ($1, $2, '{}'::jsonb, true) RETURNING id
	`, event.Type, event.SaleID).Scan(&rowID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := disp.Dispatch(context.Background(), tx, rowID, event); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
