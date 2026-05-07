package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
)

const (
	CSRFHeaderName = "X-CSRF-Token"
	CSRFFormField  = "_csrf"
)

var ErrCSRFInvalid = errors.New("auth: csrf token invalid")

type CSRFManager struct {
	secret []byte
}

func NewCSRFManager(secret []byte) *CSRFManager {
	return &CSRFManager{secret: secret}
}

func (m *CSRFManager) TokenFor(sessionToken string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(sessionToken))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (m *CSRFManager) Verify(sessionToken, presented string) bool {
	if sessionToken == "" || presented == "" {
		return false
	}
	expected := m.TokenFor(sessionToken)
	return hmac.Equal([]byte(expected), []byte(presented))
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

func (m *CSRFManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := SessionFromContext(r.Context())
		if !ok || sess.Token == "" {
			http.Error(w, "csrf: no session", http.StatusForbidden)
			return
		}
		presented := r.Header.Get(CSRFHeaderName)
		if presented == "" {
			presented = r.FormValue(CSRFFormField)
		}
		if !m.Verify(sess.Token, presented) {
			http.Error(w, "csrf: token mismatch", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
