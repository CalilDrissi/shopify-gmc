package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
)

const (
	SessionCookieName      = "session"
	SessionCookiePath      = "/"
	AdminSessionCookieName = "admin_session"
	AdminSessionCookiePath = "/admin"
)

var ErrCookieMissing = errors.New("auth: cookie missing")

type SessionCookie struct {
	SessionID uuid.UUID `json:"sid"`
	Token     string    `json:"tok"`
}

type CookieManager struct {
	sc     *securecookie.SecureCookie
	secure bool
}

func NewCookieManager(hashKey, blockKey []byte, secure bool) *CookieManager {
	sc := securecookie.New(hashKey, blockKey)
	sc.SetSerializer(securecookie.JSONEncoder{})
	return &CookieManager{sc: sc, secure: secure}
}

func (m *CookieManager) Write(w http.ResponseWriter, name, path string, v SessionCookie, expires time.Time) error {
	encoded, err := m.sc.Encode(name, v)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    encoded,
		Path:     path,
		Expires:  expires,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (m *CookieManager) Read(r *http.Request, name string) (SessionCookie, error) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return SessionCookie{}, ErrCookieMissing
	}
	var v SessionCookie
	if err := m.sc.Decode(name, cookie.Value, &v); err != nil {
		return SessionCookie{}, err
	}
	return v, nil
}

func (m *CookieManager) Clear(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
