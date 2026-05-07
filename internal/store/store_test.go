package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skipping store tests, bad DSN:", err)
		os.Exit(0)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skipping store tests, cannot connect:", err)
		os.Exit(0)
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		fmt.Fprintln(os.Stderr, "skipping store tests, ping failed:", err)
		os.Exit(0)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// withTx begins a transaction that is rolled back automatically when the test ends.
func withTx(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test DB available")
	}
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	return ctx, tx
}

// rand string for unique fixtures.
func randSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func seedUser(ctx context.Context, t *testing.T, q Querier) *User {
	t.Helper()
	u := &User{
		Email:        "user-" + randSuffix(t) + "@example.com",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$AAAA$BBBB",
	}
	if err := (UsersRepo{}).Insert(ctx, q, u); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func seedTenant(ctx context.Context, t *testing.T, q Querier) *Tenant {
	t.Helper()
	suffix := randSuffix(t)
	tn := &Tenant{Name: "Acme " + suffix, Slug: "acme-" + suffix}
	if err := (TenantsRepo{}).Insert(ctx, q, tn); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return tn
}

func seedStore(ctx context.Context, t *testing.T, q Querier, tenantID uuid.UUID) *Shop {
	t.Helper()
	s := &Shop{ShopDomain: "shop-" + randSuffix(t) + ".myshopify.com"}
	if err := (StoresRepo{}).Insert(ctx, q, tenantID, s); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	return s
}

func TestSetRequestContext(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	rc := RequestContext{TenantID: uuid.New(), UserID: uuid.New()}
	if err := SetRequestContext(ctx, tx, rc); err != nil {
		t.Fatalf("set: %v", err)
	}
	var tid, uid string
	if err := tx.QueryRow(ctx,
		`SELECT current_setting('app.current_tenant_id', true), current_setting('app.current_user_id', true)`,
	).Scan(&tid, &uid); err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if tid != rc.TenantID.String() {
		t.Errorf("tenant_id setting = %q, want %q", tid, rc.TenantID.String())
	}
	if uid != rc.UserID.String() {
		t.Errorf("user_id setting = %q, want %q", uid, rc.UserID.String())
	}
}

func TestWithRequestContext_AppliesAndCommits(t *testing.T) {
	t.Parallel()
	if testPool == nil {
		t.Skip("no test DB")
	}
	s := NewStore(testPool)
	tn := &Tenant{Name: "WRC " + randSuffix(t), Slug: "wrc-" + randSuffix(t)}
	rc := RequestContext{TenantID: uuid.Nil, UserID: uuid.Nil}

	err := s.WithRequestContext(context.Background(), rc, func(q Querier) error {
		return (TenantsRepo{}).Insert(context.Background(), q, tn)
	})
	if err != nil {
		t.Fatalf("WithRequestContext: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tn.ID)
	})
	if tn.ID == uuid.Nil {
		t.Error("expected tenant to be inserted with ID")
	}
}
