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
const defaultCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
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

func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http_request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.status),
				slog.Int64("bytes", ww.bytes),
				slog.String("remote", r.RemoteAddr),
				slog.Duration("dur", time.Since(start)),
				slog.String("request_id", RequestIDFromContext(r.Context())),
			)
		})
	}
}
