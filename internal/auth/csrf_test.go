package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCSRF_TokenForAndVerify(t *testing.T) {
	t.Parallel()
	m := NewCSRFManager([]byte("secret"))
	tok := m.TokenFor("session-token-1")
	if tok == "" {
		t.Fatal("token empty")
	}
	if !m.Verify("session-token-1", tok) {
		t.Error("expected verify ok")
	}
	if m.Verify("session-token-2", tok) {
		t.Error("token must not verify against a different session")
	}
	if m.Verify("session-token-1", tok+"x") {
		t.Error("tampered token must fail")
	}
	if m.Verify("", tok) || m.Verify("session-token-1", "") {
		t.Error("empty inputs must fail")
	}
}

func TestCSRF_TokenIsDeterministicPerSession(t *testing.T) {
	t.Parallel()
	m := NewCSRFManager([]byte("secret"))
	a := m.TokenFor("S")
	b := m.TokenFor("S")
	if a != b {
		t.Error("token should be deterministic for the same session")
	}
}

func TestCSRF_Middleware_SafeMethodsBypass(t *testing.T) {
	t.Parallel()
	m := NewCSRFManager([]byte("secret"))
	called := false
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		called = false
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/", nil)
		h.ServeHTTP(w, r)
		if w.Result().StatusCode != http.StatusOK || !called {
			t.Errorf("%s: expected pass-through, got %d called=%v", method, w.Result().StatusCode, called)
		}
	}
}

func TestCSRF_Middleware_UnsafeRequiresValidToken(t *testing.T) {
	t.Parallel()
	m := NewCSRFManager([]byte("secret"))
	sess := Session{ID: uuid.New(), Token: "abc"}
	good := m.TokenFor(sess.Token)

	cases := []struct {
		name     string
		header   string
		form     string
		wantCode int
	}{
		{"no-token", "", "", http.StatusForbidden},
		{"bad-token", "wrong", "", http.StatusForbidden},
		{"good-header", good, "", http.StatusOK},
		{"good-form", "", good, http.StatusOK},
	}

	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := ""
			if tc.form != "" {
				body = "_csrf=" + tc.form
			}
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.header != "" {
				r.Header.Set(CSRFHeaderName, tc.header)
			}
			r = r.WithContext(WithSession(r.Context(), sess))

			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Result().StatusCode != tc.wantCode {
				t.Errorf("status=%d want %d", w.Result().StatusCode, tc.wantCode)
			}
		})
	}
}

func TestCSRF_Middleware_NoSessionRejected(t *testing.T) {
	t.Parallel()
	m := NewCSRFManager([]byte("secret"))
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Result().StatusCode != http.StatusForbidden {
		t.Errorf("status=%d want 403", w.Result().StatusCode)
	}
}
