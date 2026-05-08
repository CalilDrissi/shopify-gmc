package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"
)

type ctxKey int

const requestIDKey ctxKey = iota + 1

const RequestIDHeader = "X-Request-ID"

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

func newRequestID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "0000000000000000"
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

type SecurityHeadersOptions struct {
	Production bool
	CSP        string
	HSTSMaxAge int
}

func SecurityHeaders(opts SecurityHeadersOptions) func(http.Handler) http.Handler {
	csp := opts.CSP
	if csp == "" {
		csp = defaultCSP
	}
	hsts := opts.HSTSMaxAge
	if hsts <= 0 {
		hsts = 31536000
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if opts.Production {
				h.Set("Strict-Transport-Security",
					"max-age="+itoa(hsts)+"; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Alpine.js evaluates inline expressions (e.g. `open = !open`) via Function(),
// which needs 'unsafe-eval'. The alternative is Alpine's CSP-compliant build,
// which forbids inline expressions and requires Alpine.data() registration.
//
// gumroad.com is allowlisted for the pricing/billing pages — Gumroad's
// overlay JS, CSS, fonts and images all live across gumroad.com +
// assets.gumroad.com. Audited by scripts/audit-pages.js.
const defaultCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://gumroad.com https://*.gumroad.com; " +
	"style-src 'self' 'unsafe-inline' https://gumroad.com https://*.gumroad.com; " +
	"img-src 'self' data: blob: https://*.gumroad.com https://assets.gumroad.com; " +
	"font-src 'self' data: https://*.gumroad.com https://assets.gumroad.com; " +
	"connect-src 'self' https://gumroad.com https://*.gumroad.com https://assets.gumroad.com; " +
	"form-action 'self' https://gumroad.com https://*.gumroad.com; " +
	"frame-src https://gumroad.com https://*.gumroad.com; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'; " +
	"object-src 'none'"

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (sw *statusWriter) WriteHeader(s int) {
	if !sw.wrote {
		sw.status = s
		sw.wrote = true
	}
	sw.ResponseWriter.WriteHeader(s)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wrote {
		sw.status = http.StatusOK
		sw.wrote = true
	}
	n, err := sw.ResponseWriter.Write(b)
	sw.bytes += int64(n)
	return n, err
}

// RequestLogger emits one JSON log line per request. It pulls every context
// field set by the auth + tenant + admin middlewares so callers don't have
// to thread them by hand.
//
// Probe paths (/healthz, /readyz) are silenced — load balancers spam these
// every few seconds and they'd drown the audit trail.
//
// Implementation note: middlewares lower in the stack augment context
// via `r.WithContext(...)` which produces a new *Request that the outer
// logger doesn't see (the outer's r still references the pre-augmentation
// context). To propagate the identity fields back up, RequestLogger
// installs a *requestScope value on the context which inner middlewares
// mutate. The augmented identity then comes out of the scope, not r.Context().
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isProbePath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			scope := &requestScope{}
			ctx := context.WithValue(r.Context(), requestScopeKey, scope)
			start := time.Now()
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r.WithContext(ctx))
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.status),
				slog.Int64("bytes", ww.bytes),
				slog.String("remote", r.RemoteAddr),
				slog.Duration("dur", time.Since(start)),
				slog.String("request_id", RequestIDFromContext(r.Context())),
			}
			if scope.userID != "" {
				attrs = append(attrs, slog.String("user_id", scope.userID))
			}
			if scope.tenantID != "" {
				attrs = append(attrs, slog.String("tenant_id", scope.tenantID))
			}
			if scope.adminID != "" {
				attrs = append(attrs, slog.String("platform_admin_id", scope.adminID))
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http_request", attrs...)
		})
	}
}

// requestScope is the per-request identity scratchpad inner middlewares
// write to so the outer RequestLogger can include their values without
// the standard `r.WithContext(...)` round-trip-back problem.
type requestScope struct {
	userID   string
	tenantID string
	adminID  string
}

type requestScopeKeyType struct{}

var requestScopeKey = requestScopeKeyType{}

// recordIdentityToScope is called by the auth + tenant + admin middlewares
// after they resolve the relevant principal. Safe to call when no scope is
// present (public routes, /healthz/readyz) — it just no-ops.
func recordIdentityToScope(ctx context.Context, userID, tenantID, adminID string) {
	s, ok := ctx.Value(requestScopeKey).(*requestScope)
	if !ok || s == nil {
		return
	}
	if userID != "" {
		s.userID = userID
	}
	if tenantID != "" {
		s.tenantID = tenantID
	}
	if adminID != "" {
		s.adminID = adminID
	}
}

func isProbePath(p string) bool {
	return p == "/healthz" || p == "/readyz"
}

// identityAttrs reads the per-request principal info populated by the
// auth + tenant + admin middlewares, returning slog attrs for whichever
// values are set. Used by RequestLogger and by ContextLogger.
func identityAttrs(ctx context.Context) []slog.Attr {
	var out []slog.Attr
	if u, ok := userFromContextLog(ctx); ok {
		out = append(out, slog.String("user_id", u))
	}
	if t, ok := tenantFromContextLog(ctx); ok {
		out = append(out, slog.String("tenant_id", t))
	}
	if a, ok := adminFromContextLog(ctx); ok {
		out = append(out, slog.String("platform_admin_id", a))
	}
	return out
}
