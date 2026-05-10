package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/auth"
)

type fakeUserLookup struct {
	users map[uuid.UUID]auth.User
	err   error
}

func (f *fakeUserLookup) FindUser(_ context.Context, id uuid.UUID) (auth.User, error) {
	if f.err != nil {
		return auth.User{}, f.err
	}
	u, ok := f.users[id]
	if !ok {
		return auth.User{}, errors.New("not found")
	}
	return u, nil
}

type fakeAdminLookup struct {
	admins map[uuid.UUID]bool
	err    error
}

func (f *fakeAdminLookup) IsPlatformAdmin(_ context.Context, id uuid.UUID) (bool, error) {
	return f.admins[id], f.err
}

func newCookieAndStore(t *testing.T) (*auth.CookieManager, *auth.SessionStore) {
	t.Helper()
	hashKey := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	blockKey := []byte("0123456789abcdef0123456789abcdef")
	cm := auth.NewCookieManager(hashKey, blockKey, false)
	store := auth.NewSessionStore(auth.NewMemSessionDB(), time.Hour)
	return cm, store
}

func TestRequireUser_HappyPath(t *testing.T) {
	t.Parallel()
	cm, store := newCookieAndStore(t)

	user := auth.User{ID: uuid.New(), Email: "alice@example.com", Name: "Alice"}
	users := &fakeUserLookup{users: map[uuid.UUID]auth.User{user.ID: user}}

	sess, err := store.Create(context.Background(), user.ID, "10.0.0.1", "ua")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := cm.Write(w, auth.SessionCookieName, auth.SessionCookiePath,
		auth.SessionCookie{SessionID: sess.ID, Token: sess.Token},
		time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}

	var seenUser auth.User
	var seenSess auth.Session
	h := RequireUser(cm, store, users)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUser, _ = auth.UserFromContext(r.Context())
		seenSess, _ = auth.SessionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Result().StatusCode)
	}
	if seenUser.ID != user.ID {
		t.Errorf("user.ID=%v want %v", seenUser.ID, user.ID)
	}
	if seenSess.ID != sess.ID {
		t.Errorf("sess.ID=%v want %v", seenSess.ID, sess.ID)
	}
}

func TestRequireUser_NoCookie_BrowserGetRedirects(t *testing.T) {
	t.Parallel()
	cm, store := newCookieAndStore(t)
	users := &fakeUserLookup{users: map[uuid.UUID]auth.User{}}
	h := RequireUser(cm, store, users)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler should not be called")
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/t/sarahs-shop/audits", nil)
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusFound {
		t.Errorf("status=%d want 302", got)
	}
	loc := w.Result().Header.Get("Location")
	if loc != "/login?next=%2Ft%2Fsarahs-shop%2Faudits" {
		t.Errorf("Location=%q want /login?next=%%2Ft%%2Fsarahs-shop%%2Faudits", loc)
	}
}

func TestRequireUser_NoCookie_APIClientGets401(t *testing.T) {
	t.Parallel()
	cm, store := newCookieAndStore(t)
	users := &fakeUserLookup{users: map[uuid.UUID]auth.User{}}
	h := RequireUser(cm, store, users)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler should not be called")
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/t/sarahs-shop/audits", nil)
	r.Header.Set("Accept", "application/json")
	h.ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", got)
	}
}

func TestRequireUser_NoCookie_PostStays401(t *testing.T) {
	t.Parallel()
	cm, store := newCookieAndStore(t)
	users := &fakeUserLookup{users: map[uuid.UUID]auth.User{}}
	h := RequireUser(cm, store, users)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler should not be called")
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/t/sarahs-shop/audits", nil)
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusUnauthorized {
		t.Errorf("status=%d want 401 (POST should never auto-redirect)", got)
	}
}

func TestRequirePlatformAdmin(t *testing.T) {
	t.Parallel()
	user := auth.User{ID: uuid.New()}
	cases := []struct {
		name     string
		ctx      context.Context
		admins   map[uuid.UUID]bool
		wantCode int
	}{
		{"no-user-in-ctx", context.Background(), nil, http.StatusFound},
		{"not-admin", auth.WithUser(context.Background(), user), map[uuid.UUID]bool{}, http.StatusForbidden},
		{"is-admin", auth.WithUser(context.Background(), user), map[uuid.UUID]bool{user.ID: true}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := &fakeAdminLookup{admins: tc.admins}
			h := RequirePlatformAdmin(lookup)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(tc.ctx)
			h.ServeHTTP(w, r)
			if w.Result().StatusCode != tc.wantCode {
				t.Errorf("status=%d want %d", w.Result().StatusCode, tc.wantCode)
			}
		})
	}
}
