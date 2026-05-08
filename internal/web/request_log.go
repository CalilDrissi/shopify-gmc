package web

import (
	"context"
	"log/slog"

	"github.com/example/gmcauditor/internal/auth"
)

// userFromContextLog extracts the user id (as string for slog) from the
// request context. The auth middleware stores an *auth.User value; the
// admin middleware stores the same shape via a different key. We return
// the first one we find.
func userFromContextLog(ctx context.Context) (string, bool) {
	if u, ok := auth.UserFromContext(ctx); ok && u.ID.String() != "" {
		return u.ID.String(), true
	}
	// Admin sessions store the admin user under a different key, but the
	// admin middleware also sets the auth user — covered above.
	return "", false
}

// tenantFromContextLog reads the tenant the tenant middleware loaded.
// Returns "" + false when there's no tenant context (public routes).
func tenantFromContextLog(ctx context.Context) (string, bool) {
	if t, ok := TenantFromContext(ctx); ok && t.ID.String() != "" {
		return t.ID.String(), true
	}
	return "", false
}

// adminFromContextLog returns the platform_admin id when an admin session
// is active. Used so /admin/* requests can be filtered cleanly in logs.
func adminFromContextLog(ctx context.Context) (string, bool) {
	if a, ok := ImpersonatorFromContext(ctx); ok && a.ID.String() != "" {
		return a.ID.String(), true
	}
	return "", false
}

// ContextLogger returns a slog.Logger that automatically tags every line
// with request_id / user_id / tenant_id / platform_admin_id from the
// supplied context. Handlers that already have a *slog.Logger handy
// should call this instead of `h.Logger` for non-trivial logging:
//
//   logger := web.ContextLogger(h.Logger, r.Context())
//   logger.Info("audit_enqueued", slog.String("audit_id", id.String()))
func ContextLogger(base *slog.Logger, ctx context.Context) *slog.Logger {
	if base == nil {
		base = slog.Default()
	}
	logger := base
	if id := RequestIDFromContext(ctx); id != "" {
		logger = logger.With("request_id", id)
	}
	for _, kv := range identityAttrs(ctx) {
		logger = logger.With(kv.Key, kv.Value.Any())
	}
	return logger
}
