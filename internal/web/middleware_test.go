package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestID_GeneratesAndPropagates(t *testing.T) {
	t.Parallel()
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(w, r)

	if seen == "" {
		t.Error("request id missing in context")
	}
	if got := w.Result().Header.Get(RequestIDHeader); got != seen {
		t.Errorf("response X-Request-ID=%q, ctx=%q", got, seen)
	}
}

func TestRequestID_RespectsIncomingHeader(t *testing.T) {
	t.Parallel()
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(RequestIDHeader, "trace-1234")
	h.ServeHTTP(w, r)

	if seen != "trace-1234" {
		t.Errorf("ctx id=%q, want trace-1234", seen)
	}
	if w.Result().Header.Get(RequestIDHeader) != "trace-1234" {
		t.Errorf("response id=%q, want trace-1234", w.Result().Header.Get(RequestIDHeader))
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		opts    SecurityHeadersOptions
		wantSet map[string]string
		notSet  []string
	}{
		{
			name: "dev",
			opts: SecurityHeadersOptions{Production: false},
			wantSet: map[string]string{
				"X-Content-Type-Options": "nosniff",
				"X-Frame-Options":        "DENY",
				"Referrer-Policy":        "strict-origin-when-cross-origin",
			},
			notSet: []string{"Strict-Transport-Security"},
		},
		{
			name: "prod",
			opts: SecurityHeadersOptions{Production: true},
			wantSet: map[string]string{
				"X-Content-Type-Options":    "nosniff",
				"X-Frame-Options":           "DENY",
				"Referrer-Policy":           "strict-origin-when-cross-origin",
				"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := SecurityHeaders(tc.opts)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			h.ServeHTTP(w, r)

			hdr := w.Result().Header
			if csp := hdr.Get("Content-Security-Policy"); csp == "" {
				t.Error("CSP missing")
			} else if !strings.Contains(csp, "default-src 'self'") {
				t.Errorf("CSP missing default-src 'self': %q", csp)
			}
			for k, v := range tc.wantSet {
				if got := hdr.Get(k); got != v {
					t.Errorf("%s=%q want %q", k, got, v)
				}
			}
			for _, k := range tc.notSet {
				if got := hdr.Get(k); got != "" {
					t.Errorf("%s should not be set in dev, got %q", k, got)
				}
			}
		})
	}
}

func TestRequestLogger_LogsStatusAndPath(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestID(RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	})))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/x/y", nil)
	h.ServeHTTP(w, r)

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("json log: %v\nlog=%s", err, buf.String())
	}
	if rec["msg"] != "http_request" {
		t.Errorf("msg=%v", rec["msg"])
	}
	if rec["method"] != "POST" {
		t.Errorf("method=%v", rec["method"])
	}
	if rec["path"] != "/x/y" {
		t.Errorf("path=%v", rec["path"])
	}
	if got, want := rec["status"], float64(http.StatusTeapot); got != want {
		t.Errorf("status=%v want %v", got, want)
	}
	if got, want := rec["bytes"], float64(5); got != want {
		t.Errorf("bytes=%v want %v", got, want)
	}
	if rec["request_id"] == "" {
		t.Error("request_id missing in log")
	}
}
