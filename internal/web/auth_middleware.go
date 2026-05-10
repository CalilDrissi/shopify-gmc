package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/auth"
)

// unauthorized writes a context-appropriate response when the caller isn't
// authenticated. For browser GET requests it redirects to /login with a
// `next=` parameter so the user lands back where they were trying to go.
// For everything else (POST, XHR, API clients) it returns a plain 401.
func unauthorized(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && wantsHTML(r) {
		next := safeNextURL(r.URL)
		dest := "/login"
		if next != "" {
			dest += "?next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// wantsHTML returns true when the client's Accept header prefers HTML — i.e.
// it's a browser navigation rather than an API call. Falls back to true for
// missing/empty Accept since browsers historically didn't always send one.
func wantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return true
	}
	if strings.Contains(accept, "text/html") {
		return true
	}
	if strings.Contains(accept, "application/json") || strings.Contains(accept, "application/xml") {
		return false
	}
	return strings.HasPrefix(accept, "*/*")
}

// safeNextURL returns the request's path+query if it's a same-origin
// in-app destination, otherwise an empty string. Guards against open
// redirects: refuses anything starting with "//" (protocol-relative)
// or containing a scheme.
func safeNextURL(u *url.URL) string {
	p := u.RequestURI()
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	if strings.HasPrefix(p, "/login") || strings.HasPrefix(p, "/logout") {
		return ""
	}
	return p
}

type UserLookup interface {
	FindUser(ctx context.Context, id uuid.UUID) (auth.User, error)
}

type AdminLookup interface {
	IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
}

func RequireUser(cm *auth.CookieManager, sessions *auth.SessionStore, users UserLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cv, err := cm.Read(r, auth.SessionCookieName)
			if err != nil {
				unauthorized(w, r)
				return
			}
			sess, err := sessions.Get(r.Context(), cv.Token)
			if err != nil {
				unauthorized(w, r)
				return
			}
			// When the session is an impersonation, the effective user is the
			// impersonated target — but we still expose the admin's identity
			// via WithImpersonator so the banner / audit can show "viewing as X
			// on behalf of Y".
			effectiveID := sess.UserID
			if sess.ImpersonatingUserID != nil {
				effectiveID = *sess.ImpersonatingUserID
			}
			user, err := users.FindUser(r.Context(), effectiveID)
			if err != nil {
				unauthorized(w, r)
				return
			}
			ctx := auth.WithSession(r.Context(), sess)
			ctx = auth.WithUser(ctx, user)
			adminID := ""
			if sess.ImpersonatingUserID != nil {
				if admin, err := users.FindUser(r.Context(), sess.UserID); err == nil {
					ctx = WithImpersonator(ctx, admin)
					adminID = admin.ID.String()
				}
			}
			recordIdentityToScope(ctx, user.ID.String(), "", adminID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequirePlatformAdmin(admins AdminLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				unauthorized(w, r)
				return
			}
			isAdmin, err := admins.IsPlatformAdmin(r.Context(), user.ID)
			if err != nil {
				if errors.Is(err, ErrLookupTransient) {
					http.Error(w, "service unavailable", http.StatusServiceUnavailable)
					return
				}
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if !isAdmin {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

var ErrLookupTransient = errors.New("web: lookup transient error")
