package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestCookieManager(secure bool) *CookieManager {
	hashKey := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	blockKey := []byte("0123456789abcdef0123456789abcdef")
	return NewCookieManager(hashKey, blockKey, secure)
}

func TestCookieManager_Roundtrip(t *testing.T) {
	t.Parallel()
	cm := newTestCookieManager(false)
	w := httptest.NewRecorder()

	val := SessionCookie{SessionID: uuid.New(), Token: "the-token"}
	if err := cm.Write(w, SessionCookieName, SessionCookiePath, val, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp := w.Result()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != SessionCookieName {
		t.Errorf("name=%q want %q", c.Name, SessionCookieName)
	}
	if c.Path != SessionCookiePath {
		t.Errorf("path=%q want %q", c.Path, SessionCookiePath)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must be true")
	}
	if c.Secure {
		t.Error("Secure must be false in non-prod")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite=%v want Lax", c.SameSite)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(c)
	got, err := cm.Read(r, SessionCookieName)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SessionID != val.SessionID || got.Token != val.Token {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, val)
	}
}

func TestCookieManager_AdminCookieAttributes(t *testing.T) {
	t.Parallel()
	cm := newTestCookieManager(true)
	w := httptest.NewRecorder()

	val := SessionCookie{SessionID: uuid.New(), Token: "admin-tok"}
	if err := cm.Write(w, AdminSessionCookieName, AdminSessionCookiePath, val, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := w.Result().Cookies()[0]
	if c.Name != AdminSessionCookieName {
		t.Errorf("name=%q want %q", c.Name, AdminSessionCookieName)
	}
	if c.Path != AdminSessionCookiePath {
		t.Errorf("path=%q want %q", c.Path, AdminSessionCookiePath)
	}
	if !c.Secure {
		t.Error("Secure must be true in prod")
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must be true")
	}
}

func TestCookieManager_Clear(t *testing.T) {
	t.Parallel()
	cm := newTestCookieManager(false)
	w := httptest.NewRecorder()
	cm.Clear(w, SessionCookieName, SessionCookiePath)
	c := w.Result().Cookies()[0]
	if c.MaxAge >= 0 {
		t.Errorf("MaxAge=%d, want < 0 to clear cookie", c.MaxAge)
	}
}

func TestCookieManager_TamperDetection(t *testing.T) {
	t.Parallel()
	cm := newTestCookieManager(false)
	w := httptest.NewRecorder()
	if err := cm.Write(w, SessionCookieName, "/", SessionCookie{Token: "x"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := w.Result().Cookies()[0]
	c.Value = c.Value[:len(c.Value)-2] + "XX"

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(c)
	if _, err := cm.Read(r, SessionCookieName); err == nil {
		t.Error("expected error on tampered cookie")
	}
}

func TestCookieManager_Missing(t *testing.T) {
	t.Parallel()
	cm := newTestCookieManager(false)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := cm.Read(r, SessionCookieName); err != ErrCookieMissing {
		t.Errorf("got %v, want ErrCookieMissing", err)
	}
}
