package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/auth"
)

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
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			sess, err := sessions.Get(r.Context(), cv.Token)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
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
				http.Error(w, "unauthorized", http.StatusUnauthorized)
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
				http.Error(w, "unauthorized", http.StatusUnauthorized)
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
