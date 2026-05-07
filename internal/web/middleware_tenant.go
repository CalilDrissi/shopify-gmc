package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/auth"
	"github.com/example/gmcauditor/internal/store"
)

type tenantCtxKey int

const (
	tenantKey tenantCtxKey = iota + 1
	membershipKey
	impersonationKey
	querierKey
	csrfKey
)

func WithTenant(ctx context.Context, t *store.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey, t)
}
func TenantFromContext(ctx context.Context) (*store.Tenant, bool) {
	t, ok := ctx.Value(tenantKey).(*store.Tenant)
	return t, ok
}

func WithMembership(ctx context.Context, m *store.Membership) context.Context {
	return context.WithValue(ctx, membershipKey, m)
}
func MembershipFromContext(ctx context.Context) (*store.Membership, bool) {
	m, ok := ctx.Value(membershipKey).(*store.Membership)
	return m, ok
}

func WithImpersonation(ctx context.Context, on bool) context.Context {
	return context.WithValue(ctx, impersonationKey, on)
}
func ImpersonatingFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(impersonationKey).(bool)
	return v
}

type impersonatorCtxKey struct{}

func WithImpersonator(ctx context.Context, admin auth.User) context.Context {
	return context.WithValue(ctx, impersonatorCtxKey{}, admin)
}
func ImpersonatorFromContext(ctx context.Context) (auth.User, bool) {
	v, ok := ctx.Value(impersonatorCtxKey{}).(auth.User)
	return v, ok
}

func WithQuerier(ctx context.Context, q store.Querier) context.Context {
	return context.WithValue(ctx, querierKey, q)
}
func QuerierFromContext(ctx context.Context) (store.Querier, bool) {
	q, ok := ctx.Value(querierKey).(store.Querier)
	return q, ok
}

func WithCSRF(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfKey, token)
}
func CSRFTokenFromContext(ctx context.Context) string {
	v, _ := ctx.Value(csrfKey).(string)
	return v
}

// LoadTenantBySlug reads {slug} from the path, fetches the tenant, attaches it to the context.
// Renders 404 when the slug is unknown.
func LoadTenantBySlug(pool *pgxpool.Pool, st *store.Store, render404 func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			slug := r.PathValue("slug")
			t, err := st.Tenants.GetBySlug(r.Context(), pool, slug)
			if err != nil {
				render404(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithTenant(r.Context(), t)))
		})
	}
}

// RequireMembership checks the authenticated user has a membership in the loaded tenant.
func RequireMembership(pool *pgxpool.Pool, st *store.Store, render403 func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			tenant, ok := TenantFromContext(r.Context())
			if !ok {
				render403(w, r)
				return
			}
			m, err := st.Memberships.GetByTenantAndUser(r.Context(), pool, tenant.ID, user.ID)
			if err != nil {
				render403(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithMembership(r.Context(), m)))
		})
	}
}

// CheckImpersonation reads the active session and surfaces the impersonation
// flag. RequireOwner uses this to block destructive writes during impersonation;
// the tenant layout uses it to render the red banner.
func CheckImpersonation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		on := false
		if sess, ok := auth.SessionFromContext(r.Context()); ok {
			on = sess.IsImpersonating()
		}
		next.ServeHTTP(w, r.WithContext(WithImpersonation(r.Context(), on)))
	})
}

// SetRLSContext begins a transaction, applies SET LOCAL app.current_tenant_id /
// app.current_user_id, attaches the tx to the context as the Querier, then commits
// (or rolls back on handler panic). Defense in depth alongside the explicit
// tenant_id WHERE clauses in store repos.
func SetRLSContext(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := auth.UserFromContext(r.Context())
			tenant, _ := TenantFromContext(r.Context())

			tx, err := pool.BeginTx(r.Context(), pgx.TxOptions{})
			if err != nil {
				http.Error(w, "begin tx: "+err.Error(), http.StatusInternalServerError)
				return
			}
			rc := store.RequestContext{TenantID: tenant.ID, UserID: user.ID}
			if err := store.SetRequestContext(r.Context(), tx, rc); err != nil {
				_ = tx.Rollback(r.Context())
				http.Error(w, "set rls: "+err.Error(), http.StatusInternalServerError)
				return
			}

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				if rec := recover(); rec != nil {
					_ = tx.Rollback(r.Context())
					panic(rec)
				}
				if sw.status >= 500 {
					_ = tx.Rollback(r.Context())
					return
				}
				_ = tx.Commit(r.Context())
			}()
			next.ServeHTTP(sw, r.WithContext(WithQuerier(r.Context(), tx)))
		})
	}
}

// CSRF wraps the auth.CSRFManager middleware: validates token on unsafe methods,
// and on safe methods seeds CSRFTokenFromContext for forms to render.
func CSRF(m *auth.CSRFManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		validator := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, _ := auth.SessionFromContext(r.Context())
			ctx := WithCSRF(r.Context(), m.TokenFor(sess.Token))
			next.ServeHTTP(w, r.WithContext(ctx))
		}))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			validator.ServeHTTP(w, r)
		})
	}
}

// RequireOwner gates mutating routes to owner role. Reads the membership populated by RequireMembership.
func RequireOwner(render403 func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m, ok := MembershipFromContext(r.Context())
			if !ok || m.Role != "owner" {
				render403(w, r)
				return
			}
			if ImpersonatingFromContext(r.Context()) {
				render403(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Chain composes middlewares left-to-right (outermost-first).
func Chain(mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}

var ErrNoSession = errors.New("web: no session")

// userLookupAdapter bridges UserLookup to store.Store.
type userLookupAdapter struct {
	pool *pgxpool.Pool
	st   *store.Store
}

func NewUserLookup(pool *pgxpool.Pool, st *store.Store) UserLookup {
	return &userLookupAdapter{pool: pool, st: st}
}

func (a *userLookupAdapter) FindUser(ctx context.Context, id uuid.UUID) (auth.User, error) {
	u, err := a.st.Users.GetByID(ctx, a.pool, id)
	if err != nil {
		return auth.User{}, err
	}
	name := ""
	if u.Name != nil {
		name = *u.Name
	}
	return auth.User{ID: u.ID, Email: u.Email, Name: name}, nil
}
