package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/auth"
	"github.com/example/gmcauditor/internal/billing"
	"github.com/example/gmcauditor/internal/gmc"
	"github.com/example/gmcauditor/internal/mailer"
	"github.com/example/gmcauditor/internal/store"
)

type Handlers struct {
	Pool       *pgxpool.Pool
	Store      *store.Store
	Renderer   *Renderer
	Cookies    *auth.CookieManager
	Sessions   *auth.SessionStore
	CSRF       *auth.CSRFManager
	Mailer     mailer.Mailer
	BaseURL    string
	MailFrom   string
	AppSecret  []byte // signs unsubscribe + GMC OAuth state URLs
	LoginLimit *LoginLimiter
	Logger     *slog.Logger

	// GMC integration. Wired in cmd/server when the OAuth client_id /
	// secret env vars are present; left nil for installs that don't
	// connect Google.
	GMC        *gmc.ConnectionStore
	GMCOAuth   *gmc.OAuth
	GMCBaseURL string // overrides the production Content API root for tests

	// Gumroad billing.
	Gumroad        *billing.Dispatcher
	GumroadCatalog billing.Catalog
	GumroadSecret  []byte // X-Gumroad-Signature key
}

type pageVars struct {
	Title  string
	Path   string
	User   *userView
	Tenant *tenantView
	Flash  *bannerVars
	Data   any
}

type userView struct {
	ID          uuid.UUID
	Email       string
	Name        string
	DefaultSlug string
}

type tenantView struct {
	ID   uuid.UUID
	Name string
	Slug string
	Plan string
}

type bannerVars struct {
	Variant string
	Title   string
	Message string
	Icon    string
	Action  template.HTML
}

func (h *Handlers) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	t, err := h.Renderer.Page(page)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout-"+layoutFor(page), data); err != nil {
		h.Logger.Error("render", slog.String("page", page), slog.Any("err", err))
	}
}

func layoutFor(page string) string {
	switch page {
	case "dashboard":
		return "tenant"
	case "platform-dashboard":
		return "platform"
	default:
		return "public"
	}
}

func (h *Handlers) renderError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	t, err := h.Renderer.Page("error")
	if err != nil {
		http.Error(w, msg, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.ExecuteTemplate(w, "layout-public", map[string]any{
		"Title":   fmt.Sprintf("%d", status),
		"Status":  status,
		"Message": msg,
	})
}

// ============================================================================
// Pages
// ============================================================================

func (h *Handlers) Landing(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "landing", map[string]any{"Title": "Audit your Shopify store"})
}

func (h *Handlers) Features(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "features", map[string]any{"Title": "Features"})
}

func (h *Handlers) NotFound(w http.ResponseWriter, r *http.Request) {
	h.renderError(w, http.StatusNotFound, "We couldn't find that page.")
}

// ============================================================================
// Signup
// ============================================================================

type signupFields struct {
	Email    formField
	Name     formField
	Password formField
}

type formField struct {
	Label string
	Hint  string
	Error string
	Input inputField
}

type inputField struct {
	Type, Name, ID, Value, Placeholder, Autocomplete string
	Required, Invalid, Disabled, ReadOnly             bool
	Min, Max, Step                                    string
}

func (h *Handlers) SignupForm(w http.ResponseWriter, r *http.Request) {
	// The marketing hero links here as /signup?store=<url>; pass the value
	// straight through so the template can stash it in a hidden input.
	store := strings.TrimSpace(r.URL.Query().Get("store"))
	h.render(w, r, "signup", map[string]any{
		"Title":  "Sign up",
		"Fields": defaultSignupFields(),
		"Store":  store,
	})
}

func defaultSignupFields() signupFields {
	return signupFields{
		Email:    formField{Label: "Email", Input: inputField{Type: "email", Name: "email", ID: "email", Required: true, Autocomplete: "email"}},
		Name:     formField{Label: "Your name", Input: inputField{Type: "text", Name: "name", ID: "name", Required: true, Autocomplete: "name"}},
		Password: formField{Label: "Password", Hint: "Minimum 12 characters.", Input: inputField{Type: "password", Name: "password", ID: "password", Required: true, Autocomplete: "new-password"}},
	}
}

func (h *Handlers) Signup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid form.")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	password := r.FormValue("password")
	// Marketing hero passes the store domain through as ?store=… so we can
	// derive a sensible workspace name (and later: pre-attach the store).
	storeURL := strings.TrimSpace(r.FormValue("store"))

	fields := defaultSignupFields()
	fields.Name.Input.Value = name
	fields.Email.Input.Value = email

	if name == "" || email == "" || len(password) < 12 {
		if len(password) < 12 {
			fields.Password.Error = "Password must be at least 12 characters."
			fields.Password.Input.Invalid = true
		}
		if !strings.Contains(email, "@") {
			fields.Email.Error = "Enter a valid email."
			fields.Email.Input.Invalid = true
		}
		h.render(w, r, "signup", map[string]any{"Title": "Sign up", "Fields": fields})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not hash password.")
		return
	}

	// Auto-derive workspace name + slug — no UI field for it any more.
	// Priority: store domain (from ?store=) → user's name → email local part.
	// Slug always gets a 6-char random suffix for uniqueness.
	workspace := deriveWorkspaceName(storeURL, name, email)
	slug := slugify(workspace) + "-" + base64.RawURLEncoding.EncodeToString(randBytes(4))
	rawToken, tokenHash := newToken()

	rc := store.RequestContext{}
	var newUser store.User
	var newTenant store.Tenant
	err = h.Store.WithRequestContext(r.Context(), rc, func(q store.Querier) error {
		t := &store.Tenant{Name: workspace, Slug: slug}
		if err := h.Store.Tenants.Insert(r.Context(), q, t); err != nil {
			return err
		}
		newTenant = *t

		u := &store.User{Email: email, Name: ptr(name), PasswordHash: hash, DefaultTenantID: &t.ID}
		if err := h.Store.Users.Insert(r.Context(), q, u); err != nil {
			return err
		}
		newUser = *u

		m := &store.Membership{UserID: u.ID, Role: "owner"}
		if err := h.Store.Memberships.Insert(r.Context(), q, t.ID, m); err != nil {
			return err
		}

		ev := &store.EmailVerificationToken{
			UserID:    u.ID,
			Email:     email,
			TokenHash: tokenHash,
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		if err := h.Store.EmailVerificationTokens.Insert(r.Context(), q, ev); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err, "users_email") {
			fields.Email.Error = "An account with this email already exists."
			fields.Email.Input.Invalid = true
			h.render(w, r, "signup", map[string]any{"Title": "Sign up", "Fields": fields})
			return
		}
		// Slug is auto-generated with a 6-char random suffix — collision is
		// astronomically unlikely. If it ever happens, surface as a generic
		// retry rather than crashing.
		if isUniqueViolation(err, "tenants_slug") {
			h.renderError(w, http.StatusInternalServerError, "Couldn't allocate workspace. Please retry.")
			return
		}
		h.Logger.Error("signup tx", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not create account.")
		return
	}

	// Send verification email
	if err := h.sendVerificationEmail(r.Context(), newUser, rawToken); err != nil {
		h.Logger.Error("send verification", slog.Any("err", err))
	}

	_ = newTenant
	h.render(w, r, "verify-email-pending", map[string]any{
		"Title": "Confirm your email",
		"Email": email,
	})
}

func (h *Handlers) sendVerificationEmail(ctx context.Context, u store.User, rawToken string) error {
	url := h.BaseURL + "/verify-email/" + rawToken
	name := ""
	if u.Name != nil {
		name = *u.Name
	}
	html, err := mailer.RenderVerifyEmail(mailer.VerifyEmailData{Name: name, URL: url})
	if err != nil {
		return err
	}
	return h.Mailer.Send(ctx, mailer.Compose(u.Email, h.MailFrom, "Confirm your email", html))
}

// ============================================================================
// Verify email
// ============================================================================

func (h *Handlers) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	rawToken := r.PathValue("token")
	if rawToken == "" {
		h.renderError(w, http.StatusNotFound, "Invalid verification link.")
		return
	}
	hash := sha256.Sum256([]byte(rawToken))

	var u *store.User
	err := h.Store.WithRequestContext(r.Context(), store.RequestContext{}, func(q store.Querier) error {
		tok, err := h.Store.EmailVerificationTokens.GetActiveByTokenHash(r.Context(), q, hash[:], time.Now())
		if err != nil {
			return err
		}
		if err := h.Store.EmailVerificationTokens.Consume(r.Context(), q, tok.ID, time.Now()); err != nil {
			return err
		}
		if err := h.Store.Users.MarkEmailVerified(r.Context(), q, tok.UserID, time.Now()); err != nil {
			return err
		}
		got, err := h.Store.Users.GetByID(r.Context(), q, tok.UserID)
		if err != nil {
			return err
		}
		u = got
		return nil
	})
	if err != nil {
		h.render(w, r, "verify-email", map[string]any{
			"Title": "Verification failed",
			"OK":    false,
			"Error": "This link is invalid or has expired.",
		})
		return
	}
	_ = u
	h.render(w, r, "verify-email", map[string]any{"Title": "Email confirmed", "OK": true})
}

// VerifyEmailPending renders the "check your inbox" page directly. POST
// /signup also renders this template inline; this GET route just makes
// it survive a browser refresh after signup.
func (h *Handlers) VerifyEmailPending(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	h.render(w, r, "verify-email-pending", map[string]any{
		"Title": "Confirm your email",
		"Email": email,
	})
}

func (h *Handlers) ResendVerification(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid form.")
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	// Always render success regardless of whether user exists.
	go func() {
		ctx := context.Background()
		_ = h.Store.WithRequestContext(ctx, store.RequestContext{}, func(q store.Querier) error {
			u, err := h.Store.Users.GetByEmail(ctx, q, email)
			if err != nil {
				return nil
			}
			if u.EmailVerifiedAt != nil {
				return nil
			}
			rawToken, tokenHash := newToken()
			ev := &store.EmailVerificationToken{
				UserID:    u.ID,
				Email:     u.Email,
				TokenHash: tokenHash,
				ExpiresAt: time.Now().Add(24 * time.Hour),
			}
			if err := h.Store.EmailVerificationTokens.Insert(ctx, q, ev); err != nil {
				return nil
			}
			_ = h.sendVerificationEmail(ctx, *u, rawToken)
			return nil
		})
	}()
	h.render(w, r, "verify-email-pending", map[string]any{"Title": "Resent", "Email": email})
}

// ============================================================================
// Login / Logout
// ============================================================================

type loginFields struct {
	Email    formField
	Password formField
}

func defaultLoginFields() loginFields {
	return loginFields{
		Email:    formField{Label: "Email", Input: inputField{Type: "email", Name: "email", ID: "email", Required: true, Autocomplete: "email"}},
		Password: formField{Label: "Password", Input: inputField{Type: "password", Name: "password", ID: "password", Required: true, Autocomplete: "current-password"}},
	}
}

func (h *Handlers) LoginForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "login", map[string]any{
		"Title":  "Sign in",
		"Fields": defaultLoginFields(),
	})
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid form.")
		return
	}
	ip := clientIP(r)
	if retry, ok := h.LoginLimit.Check(ip); !ok {
		h.render(w, r, "login", map[string]any{
			"Title":  "Sign in",
			"Fields": defaultLoginFields(),
			"Flash":  &bannerVars{Variant: "warning", Title: "Too many attempts", Message: fmt.Sprintf("Try again in %s.", retry.Round(time.Second))},
		})
		return
	}

	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	password := r.FormValue("password")

	var u *store.User
	err := h.Store.WithRequestContext(r.Context(), store.RequestContext{}, func(q store.Querier) error {
		got, err := h.Store.Users.GetByEmail(r.Context(), q, email)
		if err != nil {
			return err
		}
		u = got
		return nil
	})
	if err != nil {
		h.LoginLimit.RecordFailure(ip)
		h.renderLoginInvalid(w, r, email)
		return
	}
	ok, _ := auth.VerifyPassword(password, u.PasswordHash)
	if !ok {
		h.LoginLimit.RecordFailure(ip)
		h.renderLoginInvalid(w, r, email)
		return
	}
	if u.EmailVerifiedAt == nil {
		h.render(w, r, "verify-email-pending", map[string]any{"Title": "Confirm your email", "Email": email})
		return
	}

	sess, err := h.Sessions.Create(r.Context(), u.ID, ip, r.UserAgent())
	if err != nil {
		h.Logger.Error("create session", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not start session.")
		return
	}
	if err := h.Cookies.Write(w, auth.SessionCookieName, auth.SessionCookiePath,
		auth.SessionCookie{SessionID: sess.ID, Token: sess.Token},
		time.Now().Add(7*24*time.Hour),
	); err != nil {
		h.Logger.Error("write cookie", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not start session.")
		return
	}
	h.LoginLimit.RecordSuccess(ip)

	// Find primary tenant via membership.
	slug := h.firstSlug(r.Context(), u.ID)
	if slug == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/t/"+slug, http.StatusFound)
}

func (h *Handlers) renderLoginInvalid(w http.ResponseWriter, r *http.Request, email string) {
	f := defaultLoginFields()
	f.Email.Input.Value = email
	h.render(w, r, "login", map[string]any{
		"Title":  "Sign in",
		"Fields": f,
		"Flash":  &bannerVars{Variant: "critical", Title: "Sign-in failed", Message: "Invalid email or password."},
	})
}

func (h *Handlers) firstSlug(ctx context.Context, userID uuid.UUID) string {
	var slug string
	_ = h.Store.WithRequestContext(ctx, store.RequestContext{}, func(q store.Querier) error {
		// Prefer user's default tenant.
		row := q.QueryRow(ctx, `
			SELECT t.slug FROM users u
			JOIN tenants t ON t.id = u.default_tenant_id
			WHERE u.id = $1
		`, userID)
		if err := row.Scan(&slug); err == nil && slug != "" {
			return nil
		}
		// Fall back to the earliest membership.
		row = q.QueryRow(ctx, `
			SELECT t.slug FROM memberships m
			JOIN tenants t ON t.id = m.tenant_id
			WHERE m.user_id = $1
			ORDER BY m.created_at LIMIT 1
		`, userID)
		_ = row.Scan(&slug)
		return nil
	})
	return slug
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cv, err := h.Cookies.Read(r, auth.SessionCookieName); err == nil {
		_ = h.Sessions.Revoke(r.Context(), cv.SessionID)
	}
	h.Cookies.Clear(w, auth.SessionCookieName, auth.SessionCookiePath)
	http.Redirect(w, r, "/", http.StatusFound)
}

// ============================================================================
// Forgot / Reset password
// ============================================================================

func (h *Handlers) ForgotForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "forgot-password", map[string]any{
		"Title":  "Forgot password",
		"Fields": map[string]any{"Email": formField{Label: "Email", Input: inputField{Type: "email", Name: "email", ID: "email", Required: true}}},
	})
}

func (h *Handlers) Forgot(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid form.")
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	go func() {
		ctx := context.Background()
		_ = h.Store.WithRequestContext(ctx, store.RequestContext{}, func(q store.Querier) error {
			u, err := h.Store.Users.GetByEmail(ctx, q, email)
			if err != nil {
				return nil
			}
			rawToken, tokenHash := newToken()
			ip := clientIP(r)
			t := &store.PasswordResetToken{
				UserID:      u.ID,
				TokenHash:   tokenHash,
				ExpiresAt:   time.Now().Add(time.Hour),
				RequestedIP: &ip,
			}
			if err := h.Store.PasswordResetTokens.Insert(ctx, q, t); err != nil {
				return nil
			}
			html, _ := mailer.RenderPasswordReset(mailer.PasswordResetData{URL: h.BaseURL + "/reset-password/" + rawToken})
			_ = h.Mailer.Send(ctx, mailer.Compose(u.Email, h.MailFrom, "Reset your password", html))
			return nil
		})
	}()
	// Always render success regardless of whether user exists.
	h.render(w, r, "forgot-password", map[string]any{
		"Title":  "Check your email",
		"Fields": map[string]any{"Email": formField{Label: "Email", Input: inputField{Type: "email", Name: "email", ID: "email"}}},
		"Flash":  &bannerVars{Variant: "info", Title: "Check your email", Message: fmt.Sprintf("If an account exists for %s, a reset link is on its way.", email)},
	})
}

func (h *Handlers) ResetForm(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	h.render(w, r, "reset-password", map[string]any{
		"Title": "Reset password",
		"Token": token,
		"Fields": map[string]any{
			"Password": formField{Label: "New password", Input: inputField{Type: "password", Name: "password", ID: "password", Required: true, Autocomplete: "new-password"}},
			"Confirm":  formField{Label: "Confirm password", Input: inputField{Type: "password", Name: "confirm", ID: "confirm", Required: true, Autocomplete: "new-password"}},
		},
	})
}

func (h *Handlers) Reset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid form.")
		return
	}
	rawToken := r.PathValue("token")
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")
	if password != confirm {
		h.render(w, r, "reset-password", map[string]any{
			"Title": "Reset password", "Token": rawToken,
			"Fields": map[string]any{
				"Password": formField{Label: "New password", Error: "Passwords don't match.", Input: inputField{Type: "password", Name: "password", ID: "password", Required: true, Invalid: true}},
				"Confirm":  formField{Label: "Confirm password", Input: inputField{Type: "password", Name: "confirm", ID: "confirm", Required: true}},
			},
		})
		return
	}
	if len(password) < 12 {
		h.render(w, r, "reset-password", map[string]any{
			"Title": "Reset password", "Token": rawToken,
			"Fields": map[string]any{
				"Password": formField{Label: "New password", Error: "Password must be at least 12 characters.", Input: inputField{Type: "password", Name: "password", ID: "password", Required: true, Invalid: true}},
				"Confirm":  formField{Label: "Confirm password", Input: inputField{Type: "password", Name: "confirm", ID: "confirm", Required: true}},
			},
		})
		return
	}

	hash := sha256.Sum256([]byte(rawToken))
	newHash, _ := auth.HashPassword(password)

	err := h.Store.WithRequestContext(r.Context(), store.RequestContext{}, func(q store.Querier) error {
		t, err := h.Store.PasswordResetTokens.GetActiveByTokenHash(r.Context(), q, hash[:], time.Now())
		if err != nil {
			return err
		}
		if err := h.Store.Users.UpdatePassword(r.Context(), q, t.UserID, newHash); err != nil {
			return err
		}
		return h.Store.PasswordResetTokens.Consume(r.Context(), q, t.ID, time.Now())
	})
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Reset link is invalid or expired.")
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ============================================================================
// Invitation page (sign-in path lands here too).
// ============================================================================

func (h *Handlers) Invitation(w http.ResponseWriter, r *http.Request) {
	rawToken := r.PathValue("token")
	hash := sha256.Sum256([]byte(rawToken))

	var inv *store.Invitation
	var ten *store.Tenant
	err := h.Store.WithRequestContext(r.Context(), store.RequestContext{}, func(q store.Querier) error {
		got, err := h.Store.Invitations.GetByTokenHash(r.Context(), q, hash[:])
		if err != nil {
			return err
		}
		inv = got
		t, err := h.Store.Tenants.GetByID(r.Context(), q, got.TenantID)
		if err != nil {
			return err
		}
		ten = t
		return nil
	})
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Invitation not found or expired.")
		return
	}

	// Best-effort: detect a session.
	var view *userView
	if cv, err := h.Cookies.Read(r, auth.SessionCookieName); err == nil {
		if sess, err := h.Sessions.Get(r.Context(), cv.Token); err == nil {
			view = &userView{ID: sess.UserID, Email: ""} // hide email until needed
		}
	}

	h.render(w, r, "invitation", map[string]any{
		"Title":      "Invitation",
		"Tenant":     ten,
		"Invitation": inv,
		"TokenRaw":   rawToken,
		"ExpiresAt":  inv.ExpiresAt.Format("Jan 2"),
		"User":       view,
	})
}

// ============================================================================
// Helpers
// ============================================================================

func newToken() (raw string, hashed []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, h[:]
}

func ptr[T any](v T) *T { return &v }

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			v = v[:i]
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// deriveWorkspaceName picks a friendly tenant display name from whatever
// the user gave us. Order: store domain (e.g. "acme.myshopify.com" → "Acme"),
// user's first name ("Alice" → "Alice's workspace"), then email local part
// as a last resort.
func deriveWorkspaceName(storeURL, name, email string) string {
	if storeURL != "" {
		// Strip protocol + trailing path.
		s := strings.TrimSpace(storeURL)
		s = strings.TrimPrefix(s, "https://")
		s = strings.TrimPrefix(s, "http://")
		if i := strings.IndexAny(s, "/?#"); i >= 0 {
			s = s[:i]
		}
		// "acme.myshopify.com" → "acme"; "shop.acme.com" → "acme"
		parts := strings.Split(s, ".")
		var label string
		if len(parts) >= 2 {
			label = parts[0]
			if label == "shop" || label == "www" || label == "store" {
				if len(parts) >= 3 {
					label = parts[1]
				}
			}
		} else if len(parts) == 1 {
			label = parts[0]
		}
		label = strings.TrimSpace(label)
		if label != "" {
			return strings.ToUpper(label[:1]) + label[1:]
		}
	}
	if name != "" {
		first := strings.Fields(name)[0]
		return first + "'s workspace"
	}
	if i := strings.Index(email, "@"); i > 0 {
		return email[:i] + "'s workspace"
	}
	return "My workspace"
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "workspace-" + base64.RawURLEncoding.EncodeToString(randBytes(4))
	}
	return out
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func isUniqueViolation(err error, indexFragment string) bool {
	if err == nil {
		return false
	}
	var pgErr *pgErr
	if errors.As(err, &pgErr) {
		return strings.Contains(strings.ToLower(pgErr.constraintName), strings.ToLower(indexFragment))
	}
	return strings.Contains(strings.ToLower(err.Error()), strings.ToLower(indexFragment))
}

// pgErr is a minimal stand-in so we don't depend on pgconn directly here.
type pgErr struct {
	constraintName string
}

func (e *pgErr) Error() string { return "unique violation: " + e.constraintName }

var _ = pgx.ErrNoRows
