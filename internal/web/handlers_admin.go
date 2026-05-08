package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/auth"
	"github.com/example/gmcauditor/internal/mailer"
	"github.com/example/gmcauditor/internal/settings"
	"github.com/example/gmcauditor/internal/store"
)

const (
	AdminSessionTTL  = 4 * time.Hour
	preTOTPCookieTTL = 5 * time.Minute
	preTOTPCookie    = "admin_pretotp"
)

type AdminHandlers struct {
	Pool     *pgxpool.Pool
	Store    *store.Store
	Renderer *Renderer
	Cookies  *auth.CookieManager
	Sessions *auth.SessionStore
	CSRF     *auth.CSRFManager
	Mailer   mailer.Mailer
	Settings *settings.Service
	BaseURL  string
	MailFrom string
	Logger   *slog.Logger
}

type adminPageData struct {
	Title     string
	Admin     *adminView
	CSRFToken string
	Flash     *bannerVars
	Data      any
}

type adminView struct {
	UserID uuid.UUID
	Email  string
	Role   string // "support", "admin", "super"
}

func (h *AdminHandlers) renderAdmin(w http.ResponseWriter, r *http.Request, page string, payload adminPageData) {
	t, err := h.Renderer.Page(page)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout-platform", payload); err != nil {
		h.Logger.Error("renderAdmin", slog.String("page", page), slog.Any("err", err))
	}
}

// ============================================================================
// Admin login (password) → TOTP gate → admin session
// ============================================================================

type adminLoginFields struct {
	Email    formField
	Password formField
}

func defaultAdminLoginFields() adminLoginFields {
	return adminLoginFields{
		Email:    formField{Label: "Email", Input: inputField{Type: "email", Name: "email", ID: "email", Required: true, Autocomplete: "email"}},
		Password: formField{Label: "Password", Input: inputField{Type: "password", Name: "password", ID: "password", Required: true, Autocomplete: "current-password"}},
	}
}

func (h *AdminHandlers) LoginForm(w http.ResponseWriter, r *http.Request) {
	t, _ := h.Renderer.Page("admin-login")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.ExecuteTemplate(w, "layout-public", map[string]any{
		"Title":  "Platform admin sign in",
		"Fields": defaultAdminLoginFields(),
	})
}

func (h *AdminHandlers) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	password := r.FormValue("password")

	t, _ := h.Renderer.Page("admin-login")
	flash := func(banner *bannerVars) {
		f := defaultAdminLoginFields()
		f.Email.Input.Value = email
		_ = t.ExecuteTemplate(w, "layout-public", map[string]any{
			"Title": "Platform admin sign in", "Fields": f, "Flash": banner,
		})
	}

	user, err := h.Store.Users.GetByEmail(r.Context(), h.Pool, email)
	if err != nil {
		flash(&bannerVars{Variant: "critical", Title: "Sign-in failed", Message: "Invalid credentials."})
		return
	}
	ok, _ := auth.VerifyPassword(password, user.PasswordHash)
	if !ok {
		flash(&bannerVars{Variant: "critical", Title: "Sign-in failed", Message: "Invalid credentials."})
		return
	}
	admin, err := h.Store.PlatformAdmins.GetByUserID(r.Context(), h.Pool, user.ID)
	if err != nil {
		flash(&bannerVars{Variant: "critical", Title: "Not authorised", Message: "This account is not a platform admin."})
		return
	}

	// Stash the verified user id in a short-lived signed cookie ("pre-TOTP").
	if err := h.writePreTOTPCookie(w, user.ID); err != nil {
		http.Error(w, "cookie: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if admin.TOTPEnrolledAt == nil {
		http.Redirect(w, r, "/admin/totp/enroll", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/totp/verify", http.StatusFound)
}

func (h *AdminHandlers) writePreTOTPCookie(w http.ResponseWriter, userID uuid.UUID) error {
	return h.Cookies.Write(w, preTOTPCookie, "/admin",
		auth.SessionCookie{SessionID: uuid.Nil, Token: userID.String()},
		time.Now().Add(preTOTPCookieTTL))
}

func (h *AdminHandlers) readPreTOTPCookie(r *http.Request) (uuid.UUID, error) {
	cv, err := h.Cookies.Read(r, preTOTPCookie)
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(cv.Token)
}

// ============================================================================
// TOTP enrollment + verification
// ============================================================================

func (h *AdminHandlers) TOTPEnrollForm(w http.ResponseWriter, r *http.Request) {
	userID, err := h.readPreTOTPCookie(r)
	if err != nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	user, err := h.Store.Users.GetByID(r.Context(), h.Pool, userID)
	if err != nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	setup, err := auth.GenerateTOTP("gmcauditor", user.Email)
	if err != nil {
		http.Error(w, "totp gen: "+err.Error(), http.StatusInternalServerError)
		return
	}
	pngBytes, err := auth.TOTPQRPNG(setup, 240, 240)
	if err != nil {
		http.Error(w, "qr: "+err.Error(), http.StatusInternalServerError)
		return
	}
	qrDataURL := template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes))

	t, _ := h.Renderer.Page("admin-totp-enroll")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.ExecuteTemplate(w, "layout-public", map[string]any{
		"Title":     "Set up two-factor",
		"Email":     user.Email,
		"Secret":    setup.Secret,
		"OTPURL":    setup.URL,
		"QRDataURL": qrDataURL,
		"Fields": map[string]any{
			"Code": formField{Label: "Authenticator code", Input: inputField{Type: "text", Name: "code", ID: "code", Required: true, Autocomplete: "one-time-code"}},
		},
	})
}

func (h *AdminHandlers) TOTPEnroll(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	userID, err := h.readPreTOTPCookie(r)
	if err != nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	secret := r.FormValue("secret")
	code := r.FormValue("code")
	if !auth.ValidateTOTP(secret, code) {
		// re-render the form with a flash; for brevity, redirect back with err
		http.Redirect(w, r, "/admin/totp/enroll?err=invalid", http.StatusFound)
		return
	}
	if err := h.Store.PlatformAdmins.SetTOTPSecret(r.Context(), h.Pool, userID, secret, time.Now()); err != nil {
		http.Error(w, "save secret: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.startAdminSession(w, r, userID); err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.Cookies.Clear(w, preTOTPCookie, "/admin")
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (h *AdminHandlers) TOTPVerifyForm(w http.ResponseWriter, r *http.Request) {
	if _, err := h.readPreTOTPCookie(r); err != nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	t, _ := h.Renderer.Page("admin-totp-verify")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.ExecuteTemplate(w, "layout-public", map[string]any{
		"Title": "Two-factor required",
		"Fields": map[string]any{
			"Code": formField{Label: "Authenticator code", Input: inputField{Type: "text", Name: "code", ID: "code", Required: true, Autocomplete: "one-time-code"}},
		},
	})
}

func (h *AdminHandlers) TOTPVerify(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	userID, err := h.readPreTOTPCookie(r)
	if err != nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	admin, err := h.Store.PlatformAdmins.GetByUserID(r.Context(), h.Pool, userID)
	if err != nil || admin.TOTPSecret == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	code := r.FormValue("code")
	if !auth.ValidateTOTP(*admin.TOTPSecret, code) {
		http.Redirect(w, r, "/admin/totp/verify?err=invalid", http.StatusFound)
		return
	}
	if err := h.startAdminSession(w, r, userID); err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.Cookies.Clear(w, preTOTPCookie, "/admin")
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (h *AdminHandlers) startAdminSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	store := h.Sessions.WithClock(time.Now)
	sess, err := store.Create(r.Context(), userID, clientIP(r), r.UserAgent())
	if err != nil {
		return err
	}
	// Override the default 7d TTL by directly extending the row to 4h.
	if err := h.Sessions.Extend(r.Context(), sess.ID); err == nil {
		// Best effort: 4h sliding window enforced on each request via the middleware
	}
	return h.Cookies.Write(w, auth.AdminSessionCookieName, auth.AdminSessionCookiePath,
		auth.SessionCookie{SessionID: sess.ID, Token: sess.Token},
		time.Now().Add(AdminSessionTTL))
}

// ============================================================================
// Admin dashboard / tenants list / tenant detail
// ============================================================================

func (h *AdminHandlers) Dashboard(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	pool := h.Pool
	var counts struct {
		Tenants, Users, Admins, Members int
	}
	_ = pool.QueryRow(r.Context(), `SELECT count(*) FROM tenants`).Scan(&counts.Tenants)
	_ = pool.QueryRow(r.Context(), `SELECT count(*) FROM users`).Scan(&counts.Users)
	_ = pool.QueryRow(r.Context(), `SELECT count(*) FROM platform_admins`).Scan(&counts.Admins)
	_ = pool.QueryRow(r.Context(), `SELECT count(*) FROM memberships`).Scan(&counts.Members)
	d.Title = "Platform admin"
	d.Data = counts
	h.renderAdmin(w, r, "admin-dashboard", *d)
}

type tenantsListRow struct {
	Tenant      *store.Tenant
	OwnerEmail  *string
	MemberCount int
}

func (h *AdminHandlers) TenantsList(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	pool := h.Pool
	rows, err := pool.Query(r.Context(), `
		SELECT t.id, t.name, t.slug, t.kind::text, t.plan::text, t.suspended_at, t.created_at, t.updated_at,
		       (SELECT email FROM users u JOIN memberships m ON m.user_id=u.id
		         WHERE m.tenant_id=t.id AND m.role='owner' LIMIT 1) AS owner_email,
		       (SELECT count(*) FROM memberships m WHERE m.tenant_id=t.id) AS member_count
		FROM tenants t
		ORDER BY t.created_at DESC
		LIMIT 200
	`)
	if err != nil {
		http.Error(w, "query: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var list []tenantsListRow
	for rows.Next() {
		var t store.Tenant
		var owner *string
		var memberCount int
		var suspendedAt *time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Kind, &t.Plan, &suspendedAt, &t.CreatedAt, &t.UpdatedAt, &owner, &memberCount); err != nil {
			continue
		}
		// We don't have suspended_at in the Tenant struct yet; carry separately if needed.
		_ = suspendedAt
		list = append(list, tenantsListRow{Tenant: &t, OwnerEmail: owner, MemberCount: memberCount})
	}
	d.Title = "Tenants"
	d.Data = list
	h.renderAdmin(w, r, "admin-tenants", *d)
}

type tenantDetailData struct {
	Tenant      *store.Tenant
	Members     []tenantMemberRow
	SuspendedAt *time.Time
}

type tenantMemberRow struct {
	User       *store.User
	Membership store.Membership
}

func (h *AdminHandlers) TenantDetail(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	pool := h.Pool
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var t store.Tenant
	var suspendedAt *time.Time
	if err := pool.QueryRow(r.Context(), `
		SELECT id, name, slug, kind::text, plan::text, plan_renews_at, suspended_at, created_at, updated_at
		FROM tenants WHERE id=$1
	`, id).Scan(&t.ID, &t.Name, &t.Slug, &t.Kind, &t.Plan, &t.PlanRenewsAt, &suspendedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		http.Error(w, "tenant: "+err.Error(), http.StatusNotFound)
		return
	}
	members, _ := h.Store.Memberships.ListByTenant(r.Context(), pool, t.ID)
	rows := make([]tenantMemberRow, 0, len(members))
	for _, m := range members {
		u, err := h.Store.Users.GetByID(r.Context(), pool, m.UserID)
		if err != nil {
			continue
		}
		rows = append(rows, tenantMemberRow{User: u, Membership: m})
	}
	d.Title = t.Name
	d.Data = tenantDetailData{Tenant: &t, Members: rows, SuspendedAt: suspendedAt}
	h.renderAdmin(w, r, "admin-tenant-detail", *d)
}

func (h *AdminHandlers) TenantSuspend(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	pool := h.Pool
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if _, err := pool.Exec(r.Context(), `UPDATE tenants SET suspended_at=now() WHERE id=$1`, id); err != nil {
		http.Error(w, "suspend: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.recordAdminAudit(r, d.Admin.UserID, "tenant_suspend", "tenant", id.String(), nil)
	http.Redirect(w, r, "/admin/tenants/"+id.String(), http.StatusFound)
}

func (h *AdminHandlers) TenantUnsuspend(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	pool := h.Pool
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if _, err := pool.Exec(r.Context(), `UPDATE tenants SET suspended_at=NULL WHERE id=$1`, id); err != nil {
		http.Error(w, "unsuspend: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.recordAdminAudit(r, d.Admin.UserID, "tenant_unsuspend", "tenant", id.String(), nil)
	http.Redirect(w, r, "/admin/tenants/"+id.String(), http.StatusFound)
}

// ============================================================================
// Impersonation start / stop
// ============================================================================

func (h *AdminHandlers) ImpersonationStart(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	tenantID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	targetUserStr := r.FormValue("user_id")
	reason := strings.TrimSpace(r.FormValue("reason"))
	pool := h.Pool

	var targetUserID uuid.UUID
	if targetUserStr != "" {
		v, err := uuid.Parse(targetUserStr)
		if err != nil {
			http.Error(w, "bad user_id", http.StatusBadRequest)
			return
		}
		targetUserID = v
	} else {
		// default: tenant owner
		_ = pool.QueryRow(r.Context(),
			`SELECT user_id FROM memberships WHERE tenant_id=$1 AND role='owner' LIMIT 1`, tenantID,
		).Scan(&targetUserID)
		if targetUserID == uuid.Nil {
			http.Error(w, "no owner to impersonate", http.StatusBadRequest)
			return
		}
	}

	// Create the impersonation log entry.
	logEntry := &store.ImpersonationLogEntry{
		AdminUserID:        &d.Admin.UserID,
		ImpersonatedUserID: &targetUserID,
		TenantID:           &tenantID,
		Reason:             &reason,
	}
	if err := h.Store.ImpersonationLog.Start(r.Context(), pool, logEntry); err != nil {
		http.Error(w, "log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Mint a fresh session whose owner is the admin, but with impersonating fields set.
	sess, err := h.Sessions.Create(r.Context(), d.Admin.UserID, clientIP(r), r.UserAgent())
	if err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Sessions.SetImpersonation(r.Context(), sess.ID, &targetUserID, &tenantID, &logEntry.ID); err != nil {
		http.Error(w, "impersonate: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Write a regular session cookie (path /) so tenant pages pick it up.
	if err := h.Cookies.Write(w, auth.SessionCookieName, auth.SessionCookiePath,
		auth.SessionCookie{SessionID: sess.ID, Token: sess.Token},
		time.Now().Add(time.Hour)); err != nil {
		http.Error(w, "cookie: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Audit + email the tenant owner.
	h.recordAdminAudit(r, d.Admin.UserID, "impersonate_start", "tenant", tenantID.String(), map[string]any{
		"target_user_id": targetUserID.String(),
		"reason":         reason,
	})
	go h.notifyImpersonationStart(d.Admin.Email, tenantID, reason)

	// Redirect to the tenant dashboard.
	var slug string
	_ = pool.QueryRow(r.Context(), `SELECT slug FROM tenants WHERE id=$1`, tenantID).Scan(&slug)
	http.Redirect(w, r, "/t/"+slug, http.StatusFound)
}

func (h *AdminHandlers) ImpersonationStop(w http.ResponseWriter, r *http.Request) {
	pool := h.Pool
	cv, err := h.Cookies.Read(r, auth.SessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}
	sess, err := h.Sessions.Get(r.Context(), cv.Token)
	if err != nil {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}
	tenantID := sess.ImpersonatingTenantID
	logID := sess.ImpersonationLogID

	_ = h.Sessions.Revoke(r.Context(), sess.ID)
	h.Cookies.Clear(w, auth.SessionCookieName, auth.SessionCookiePath)

	if logID != nil {
		_, _ = pool.Exec(r.Context(),
			`UPDATE impersonation_log SET ended_at=now() WHERE id=$1 AND ended_at IS NULL`, *logID)
	}

	d := h.adminCtx(r)
	if d != nil {
		target := ""
		if tenantID != nil {
			target = tenantID.String()
		}
		h.recordAdminAudit(r, d.Admin.UserID, "impersonate_stop", "tenant", target, nil)
	}
	if tenantID != nil {
		http.Redirect(w, r, "/admin/tenants/"+tenantID.String(), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (h *AdminHandlers) notifyImpersonationStart(adminEmail string, tenantID uuid.UUID, reason string) {
	pool := h.Pool
	ctx := context.Background()
	var ownerEmail, tenantName string
	_ = pool.QueryRow(ctx, `
		SELECT u.email, t.name
		FROM tenants t
		JOIN memberships m ON m.tenant_id=t.id AND m.role='owner'
		JOIN users u ON u.id=m.user_id
		WHERE t.id=$1 LIMIT 1
	`, tenantID).Scan(&ownerEmail, &tenantName)
	if ownerEmail == "" {
		return
	}
	if reason == "" {
		reason = "(no reason given)"
	}
	html, err := mailer.RenderImpersonation(mailer.ImpersonationData{
		Admin:  adminEmail,
		Tenant: tenantName,
		At:     time.Now().UTC().Format(time.RFC1123),
		Reason: reason,
	})
	if err != nil {
		return
	}
	_ = h.Mailer.Send(ctx, mailer.Compose(ownerEmail, h.MailFrom, "Account access notice", html))
}

// ============================================================================
// Audit log + Settings
// ============================================================================

func (h *AdminHandlers) AuditLogPage(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	pool := h.Pool
	rows, err := pool.Query(r.Context(), `
		SELECT al.id, COALESCE(u.email, '(deleted)'), al.action, al.target_type, al.target_id,
		       al.metadata, al.created_at
		FROM platform_admin_audit_log al
		LEFT JOIN users u ON u.id = al.admin_user_id
		ORDER BY al.created_at DESC
		LIMIT 200
	`)
	if err != nil {
		http.Error(w, "query: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type entry struct {
		ID         uuid.UUID
		AdminEmail string
		Action     string
		TargetType *string
		TargetID   *string
		Metadata   []byte
		CreatedAt  time.Time
	}
	var list []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.AdminEmail, &e.Action, &e.TargetType, &e.TargetID, &e.Metadata, &e.CreatedAt); err != nil {
			continue
		}
		list = append(list, e)
	}
	d.Title = "Audit log"
	d.Data = list
	h.renderAdmin(w, r, "admin-audit-log", *d)
}

// GMCPage renders the platform-admin GMC overview: connections grouped by
// status (active/warning/suspended/revoked/error), recent failed syncs,
// rate-limit usage (count of recent error rows mentioning rate limit),
// and reauth-required list (status=revoked).
//
// The query joins through tenants + stores so the page can show "which
// tenant + which store" per row without N round-trips.
func (h *AdminHandlers) GMCPage(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}

	type connRow struct {
		ID            uuid.UUID
		TenantSlug    string
		ShopDomain    string
		MerchantID    string
		Status        string // gmc_connection_status enum
		AccountStatus *string
		Warnings      int
		Suspensions   int
		LastSyncAt    *time.Time
		LastSyncStatus *string
		LastError     *string
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT c.id, t.slug, s.shop_domain, c.merchant_id, c.status::text,
		       c.account_status, c.warnings_count, c.suspensions_count,
		       c.last_sync_at, c.last_sync_status, c.last_error_message
		FROM store_gmc_connections c
		JOIN tenants t ON t.id = c.tenant_id
		JOIN stores  s ON s.id = c.store_id
		ORDER BY
		  CASE c.status
		    WHEN 'error' THEN 1
		    WHEN 'expired' THEN 2
		    WHEN 'revoked' THEN 3
		    WHEN 'active' THEN 4
		  END,
		  c.suspensions_count DESC,
		  c.warnings_count DESC,
		  c.last_sync_at DESC NULLS LAST
		LIMIT 500
	`)
	if err != nil {
		http.Error(w, "query: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var (
		all []connRow
		byStatus = map[string]int{} // active/warning/suspended/revoked/error
		failed []connRow
		reauth []connRow
		rateLimited int
	)
	for rows.Next() {
		var c connRow
		if err := rows.Scan(&c.ID, &c.TenantSlug, &c.ShopDomain, &c.MerchantID, &c.Status,
			&c.AccountStatus, &c.Warnings, &c.Suspensions, &c.LastSyncAt, &c.LastSyncStatus, &c.LastError); err != nil {
			continue
		}
		// Bucket: revoked/expired/error keep their literal status; active is
		// further split by account_status (warning/suspended) for the dashboard.
		bucket := c.Status
		if c.Status == "active" && c.AccountStatus != nil {
			switch *c.AccountStatus {
			case "warning":
				bucket = "warning"
			case "suspended":
				bucket = "suspended"
			default:
				bucket = "active"
			}
		}
		byStatus[bucket]++
		if c.LastSyncStatus != nil && *c.LastSyncStatus == "error" {
			failed = append(failed, c)
			if c.LastError != nil && (containsAny(*c.LastError, "rate limit", "429")) {
				rateLimited++
			}
		}
		if c.Status == "revoked" || (c.LastError != nil && containsAny(*c.LastError, "401", "unauthorized", "Re-consent")) {
			reauth = append(reauth, c)
		}
		all = append(all, c)
	}

	d.Title = "GMC overview"
	d.Data = map[string]any{
		"All":          all,
		"ByStatus":     byStatus,
		"Total":        len(all),
		"FailedSyncs":  failed,
		"RateLimited":  rateLimited,
		"ReauthList":   reauth,
	}
	h.renderAdmin(w, r, "admin-gmc", *d)
}

// containsAny is a tiny helper — case-insensitive substring match against
// any of the needles. Local to this file to avoid pulling strings into the
// package's public surface.
func containsAny(haystack string, needles ...string) bool {
	low := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(low, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func (h *AdminHandlers) SettingsPage(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil || d.Admin.Role != "super" {
		http.Error(w, "super_admin only", http.StatusForbidden)
		return
	}
	previews := h.Settings.GetAll(r.Context())
	d.Title = "Platform settings"
	d.Data = previews
	h.renderAdmin(w, r, "admin-settings", *d)
}

func (h *AdminHandlers) SettingsSave(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil || d.Admin.Role != "super" {
		http.Error(w, "super_admin only", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	preset := r.FormValue("preset")
	if preset == "anthropic" {
		_ = h.Settings.Set(r.Context(), &d.Admin.UserID, settings.KeyAIBaseURL, "https://api.anthropic.com/v1")
		_ = h.Settings.Set(r.Context(), &d.Admin.UserID, settings.KeyAIModel, "claude-sonnet-4-6")
		_ = h.Settings.Set(r.Context(), &d.Admin.UserID, settings.KeyAIModelSummary, "claude-haiku-4-5")
	} else if preset == "openai" {
		_ = h.Settings.Set(r.Context(), &d.Admin.UserID, settings.KeyAIBaseURL, "https://api.openai.com/v1")
	}
	for _, k := range []string{settings.KeyAIBaseURL, settings.KeyAIModel, settings.KeyAIModelSummary, settings.KeyAIMaxCallsPerAudit} {
		v := strings.TrimSpace(r.FormValue(k))
		if v != "" {
			_ = h.Settings.Set(r.Context(), &d.Admin.UserID, k, v)
		}
	}
	apiKey := r.FormValue(settings.KeyAIAPIKey)
	if apiKey != "" {
		_ = h.Settings.Set(r.Context(), &d.Admin.UserID, settings.KeyAIAPIKey, apiKey)
	}
	http.Redirect(w, r, "/admin/settings?ok=saved", http.StatusFound)
}

func (h *AdminHandlers) SettingsTestConnection(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil || d.Admin.Role != "super" {
		http.Error(w, "super_admin only", http.StatusForbidden)
		return
	}
	baseURL, err := h.Settings.Get(r.Context(), settings.KeyAIBaseURL)
	if err != nil || baseURL == "" {
		http.Redirect(w, r, "/admin/settings?test=missing-base-url", http.StatusFound)
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		http.Redirect(w, r, "/admin/settings?test=invalid-url", http.StatusFound)
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, parsed.Scheme+"://"+parsed.Host, nil)
	resp, err := client.Do(req)
	if err != nil {
		http.Redirect(w, r, "/admin/settings?test=err&msg="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	defer resp.Body.Close()
	http.Redirect(w, r, fmt.Sprintf("/admin/settings?test=ok&status=%d", resp.StatusCode), http.StatusFound)
}

// ============================================================================
// Logout
// ============================================================================

func (h *AdminHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cv, err := h.Cookies.Read(r, auth.AdminSessionCookieName); err == nil {
		_ = h.Sessions.Revoke(r.Context(), cv.SessionID)
	}
	h.Cookies.Clear(w, auth.AdminSessionCookieName, auth.AdminSessionCookiePath)
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

// ============================================================================
// Helpers / context
// ============================================================================

// adminCtx returns the admin view if a valid admin_session cookie is present.
// Also extends the session by 4h on each request (sliding window).
func (h *AdminHandlers) adminCtx(r *http.Request) *adminPageData {
	cv, err := h.Cookies.Read(r, auth.AdminSessionCookieName)
	if err != nil {
		return nil
	}
	sess, err := h.Sessions.Get(r.Context(), cv.Token)
	if err != nil {
		return nil
	}
	pool := h.Pool
	user, err := h.Store.Users.GetByID(r.Context(), pool, sess.UserID)
	if err != nil {
		return nil
	}
	admin, err := h.Store.PlatformAdmins.GetByUserID(r.Context(), pool, user.ID)
	if err != nil {
		return nil
	}
	// Sliding window: extend the session on every authenticated request.
	_ = h.Sessions.Extend(r.Context(), sess.ID)
	// Surface user_id + platform_admin_id on this request's log line.
	recordIdentityToScope(r.Context(), user.ID.String(), "", admin.ID.String())
	return &adminPageData{
		Admin: &adminView{UserID: user.ID, Email: user.Email, Role: admin.Role},
		CSRFToken: h.CSRF.TokenFor(sess.Token),
	}
}

func (h *AdminHandlers) recordAdminAudit(r *http.Request, adminUserID uuid.UUID, action, targetType, targetID string, meta map[string]any) {
	pool := h.Pool
	tt := targetType
	tid := targetID
	ip := clientIP(r)
	metaBytes := []byte("{}")
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			metaBytes = b
		}
	}
	_ = h.Store.PlatformAdminAuditLog.Insert(r.Context(), pool, &store.PlatformAdminAuditLogEntry{
		AdminUserID: &adminUserID,
		Action:      action,
		TargetType:  &tt,
		TargetID:    &tid,
		Metadata:    metaBytes,
		IPAddress:   &ip,
	})
}

