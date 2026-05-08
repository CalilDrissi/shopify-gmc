package web

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/auth"
	"github.com/example/gmcauditor/internal/mailer"
	"github.com/example/gmcauditor/internal/store"
)

type tenantPageData struct {
	Title         string
	User          *store.User
	Tenant        *store.Tenant
	Membership    *store.Membership
	CSRFToken     string
	Memberships   []store.MembershipWithTenant
	IsOwner       bool
	Impersonating bool
	Impersonator  *auth.User
	Flash         *bannerVars
	Data          any
}

func (h *Handlers) buildTenantData(r *http.Request) tenantPageData {
	d := tenantPageData{CSRFToken: CSRFTokenFromContext(r.Context())}
	if t, ok := TenantFromContext(r.Context()); ok {
		d.Tenant = t
	}
	if m, ok := MembershipFromContext(r.Context()); ok {
		d.Membership = m
		d.IsOwner = m.Role == "owner"
	}
	if u, ok := auth.UserFromContext(r.Context()); ok {
		// We have the auth.User (lightweight); pull the full one for templates.
		if q, ok := QuerierFromContext(r.Context()); ok {
			if full, err := h.Store.Users.GetByID(r.Context(), q, u.ID); err == nil {
				d.User = full
			}
		}
	}
	if d.User != nil {
		if q, ok := QuerierFromContext(r.Context()); ok {
			if mems, err := h.Store.Users.ListMemberships(r.Context(), q, d.User.ID); err == nil {
				d.Memberships = mems
			}
		}
	}
	d.Impersonating = ImpersonatingFromContext(r.Context())
	if admin, ok := ImpersonatorFromContext(r.Context()); ok {
		a := admin
		d.Impersonator = &a
	}
	return d
}

func (h *Handlers) renderTenant(w http.ResponseWriter, r *http.Request, page string, payload tenantPageData) {
	t, err := h.Renderer.Page(page)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout-tenant", payload); err != nil {
		h.Logger.Error("renderTenant", slog.String("page", page), slog.Any("err", err))
	}
}

// ============================================================================
// Dashboard
// ============================================================================

func (h *Handlers) Dashboard(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	d.Title = "Dashboard"

	// GMC summary: count of stores + connected + with active warnings.
	// Includes 'error' so the "needs attention" total reflects API failures
	// alongside Google-reported warnings.
	type gmcSummary struct {
		TotalStores     int
		Connected       int
		WithWarnings    int
		WithSuspensions int
	}
	var s gmcSummary
	_ = h.Pool.QueryRow(r.Context(), `
		SELECT
			(SELECT count(*) FROM stores WHERE tenant_id = $1),
			(SELECT count(*) FROM store_gmc_connections WHERE tenant_id = $1 AND status = 'active'),
			(SELECT count(*) FROM store_gmc_connections WHERE tenant_id = $1 AND status = 'active' AND warnings_count > 0),
			(SELECT count(*) FROM store_gmc_connections WHERE tenant_id = $1 AND status = 'active' AND suspensions_count > 0)
	`, d.Tenant.ID).Scan(&s.TotalStores, &s.Connected, &s.WithWarnings, &s.WithSuspensions)

	d.Data = map[string]any{
		"GMC": s,
	}
	h.renderTenant(w, r, "dashboard", d)
}

// ============================================================================
// Members
// ============================================================================

func (h *Handlers) MembersPage(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	d.Title = "Members"

	q, _ := QuerierFromContext(r.Context())
	members, _ := h.Store.Memberships.ListByTenant(r.Context(), q, d.Tenant.ID)
	pending, _ := h.Store.Invitations.ListPendingByTenant(r.Context(), q, d.Tenant.ID)

	memberRows := make([]map[string]any, 0, len(members))
	for _, m := range members {
		userRow, _ := h.Store.Users.GetByID(r.Context(), q, m.UserID)
		memberRows = append(memberRows, map[string]any{
			"Membership": m,
			"User":       userRow,
		})
	}
	d.Data = map[string]any{
		"Members":     memberRows,
		"Invitations": pending,
		"InviteForm": formField{
			Label: "Email",
			Input: inputField{Type: "email", Name: "email", ID: "invite-email", Required: true, Placeholder: "teammate@example.com"},
		},
	}
	h.renderTenant(w, r, "members", d)
}

func (h *Handlers) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	d := h.buildTenantData(r)
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	role := r.FormValue("role")
	if role == "" {
		role = "member"
	}
	if email == "" || !strings.Contains(email, "@") {
		http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?err=invalid-email", http.StatusFound)
		return
	}
	if e := h.EnforcePlanLimit(r.Context(), d.Tenant.ID, string(d.Tenant.Plan), ResMembers); e != nil {
		h.RenderPlanLimit(w, r, e)
		return
	}

	q, _ := QuerierFromContext(r.Context())
	rawToken, tokenHash := newToken()
	inv := &store.Invitation{
		InviterID: &d.User.ID,
		Email:     email,
		Role:      role,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}
	if err := h.Store.Invitations.Insert(r.Context(), q, d.Tenant.ID, inv); err != nil {
		h.Logger.Error("create invitation", slog.Any("err", err))
		http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?err=insert", http.StatusFound)
		return
	}

	// Best-effort email send. Captured by tx so even if smtp fails we still see the row.
	go h.sendInvitationEmail(d.Tenant, d.User, inv, rawToken)

	http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?ok=sent", http.StatusFound)
}

func (h *Handlers) sendInvitationEmail(tenant *store.Tenant, inviter *store.User, inv *store.Invitation, rawToken string) {
	url := h.BaseURL + "/invitations/" + rawToken
	inviterName := inviter.Email
	if inviter.Name != nil && *inviter.Name != "" {
		inviterName = *inviter.Name
	}
	html, err := mailer.RenderInvitation(mailer.InvitationData{
		Tenant: tenant.Name, Inviter: inviterName, Role: inv.Role, URL: url,
	})
	if err != nil {
		return
	}
	_ = h.Mailer.Send(context.Background(), mailer.Compose(inv.Email, h.MailFrom, "You're invited to "+tenant.Name, html))
}

func (h *Handlers) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	q, _ := QuerierFromContext(r.Context())
	if err := h.Store.Invitations.Revoke(r.Context(), q, d.Tenant.ID, id); err != nil {
		h.Logger.Error("revoke invitation", slog.Any("err", err))
	}
	http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members", http.StatusFound)
}

func (h *Handlers) RemoveMembership(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	idStr := r.PathValue("user_id")
	uid, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if uid == d.User.ID {
		// owner removing self is blocked; use transfer-ownership first
		http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?err=cannot-remove-self", http.StatusFound)
		return
	}
	q, _ := QuerierFromContext(r.Context())
	if err := h.Store.Memberships.Remove(r.Context(), q, d.Tenant.ID, uid); err != nil {
		h.Logger.Error("remove membership", slog.Any("err", err))
	}
	http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members", http.StatusFound)
}

// ============================================================================
// Transfer ownership / Delete tenant
// ============================================================================

func (h *Handlers) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	d := h.buildTenantData(r)
	password := r.FormValue("password")
	newOwnerStr := r.FormValue("user_id")
	if password == "" || newOwnerStr == "" {
		http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?err=missing", http.StatusFound)
		return
	}

	ok, _ := auth.VerifyPassword(password, d.User.PasswordHash)
	if !ok {
		http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?err=password", http.StatusFound)
		return
	}

	newOwnerID, err := uuid.Parse(newOwnerStr)
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}

	q, _ := QuerierFromContext(r.Context())
	// Demote current owner to member, promote target to owner.
	// Constraint: one-owner-per-tenant unique partial index.
	// We do it in two steps inside the same tx.
	if _, err := q.Exec(r.Context(),
		`UPDATE memberships SET role='member', updated_at=now() WHERE tenant_id=$1 AND user_id=$2`,
		d.Tenant.ID, d.User.ID); err != nil {
		h.Logger.Error("demote", slog.Any("err", err))
		http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?err=demote", http.StatusFound)
		return
	}
	if _, err := q.Exec(r.Context(),
		`UPDATE memberships SET role='owner', updated_at=now() WHERE tenant_id=$1 AND user_id=$2`,
		d.Tenant.ID, newOwnerID); err != nil {
		h.Logger.Error("promote", slog.Any("err", err))
		http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?err=promote", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/t/"+d.Tenant.Slug+"/members?ok=transferred", http.StatusFound)
}

func (h *Handlers) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	d := h.buildTenantData(r)
	password := r.FormValue("password")
	confirmName := r.FormValue("confirm_name")

	ok, _ := auth.VerifyPassword(password, d.User.PasswordHash)
	if !ok {
		http.Redirect(w, r, "/account?err=password", http.StatusFound)
		return
	}
	if confirmName != d.Tenant.Name {
		http.Redirect(w, r, "/account?err=confirm-name", http.StatusFound)
		return
	}

	q, _ := QuerierFromContext(r.Context())
	if _, err := q.Exec(r.Context(), `DELETE FROM tenants WHERE id=$1`, d.Tenant.ID); err != nil {
		h.Logger.Error("delete tenant", slog.Any("err", err))
		http.Redirect(w, r, "/account?err=delete", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/account?ok=deleted", http.StatusFound)
}

// ============================================================================
// Account / Switcher
// ============================================================================

func (h *Handlers) AccountPage(w http.ResponseWriter, r *http.Request) {
	cv, err := h.Cookies.Read(r, auth.SessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	sess, err := h.Sessions.Get(r.Context(), cv.Token)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	user, err := h.Store.Users.GetByID(r.Context(), h.Pool, sess.UserID)
	if err != nil {
		http.Error(w, "user lookup failed", http.StatusInternalServerError)
		return
	}
	memberships, _ := h.Store.Users.ListMemberships(r.Context(), h.Pool, user.ID)

	csrfToken := h.CSRF.TokenFor(sess.Token)
	t, err := h.Renderer.Page("account")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.ExecuteTemplate(w, "layout-public", map[string]any{
		"Title":       "Account",
		"User":        user,
		"Memberships": memberships,
		"CSRFToken":   csrfToken,
	})
}

func (h *Handlers) SwitchTenant(w http.ResponseWriter, r *http.Request) {
	cv, err := h.Cookies.Read(r, auth.SessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	sess, err := h.Sessions.Get(r.Context(), cv.Token)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	tidStr := r.PathValue("tenant_id")
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		http.Error(w, "bad tenant id", http.StatusBadRequest)
		return
	}
	// Verify membership before switching.
	if _, err := h.Store.Memberships.GetByTenantAndUser(r.Context(), h.Pool, tid, sess.UserID); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := h.Store.Users.SetDefaultTenant(r.Context(), h.Pool, sess.UserID, &tid); err != nil {
		http.Error(w, "switch failed", http.StatusInternalServerError)
		return
	}
	t, err := h.Store.Tenants.GetByID(r.Context(), h.Pool, tid)
	if err != nil {
		http.Redirect(w, r, "/account", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/t/"+t.Slug, http.StatusFound)
}

// ============================================================================
// Invitation accept (signed-in path)
// ============================================================================

func (h *Handlers) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	rawToken := r.PathValue("token")
	hash := sha256.Sum256([]byte(rawToken))

	cv, err := h.Cookies.Read(r, auth.SessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login?invite="+rawToken, http.StatusFound)
		return
	}
	sess, err := h.Sessions.Get(r.Context(), cv.Token)
	if err != nil {
		http.Redirect(w, r, "/login?invite="+rawToken, http.StatusFound)
		return
	}

	var (
		acceptedTenant *store.Tenant
	)
	err = h.Store.WithRequestContext(r.Context(), store.RequestContext{}, func(q store.Querier) error {
		inv, err := h.Store.Invitations.GetByTokenHash(r.Context(), q, hash[:])
		if err != nil {
			return err
		}
		if inv.Status != "pending" || inv.ExpiresAt.Before(time.Now()) {
			return errors.New("invitation no longer valid")
		}
		// Attach session user as new membership.
		m := &store.Membership{UserID: sess.UserID, Role: inv.Role}
		if err := h.Store.Memberships.Insert(r.Context(), q, inv.TenantID, m); err != nil {
			return err
		}
		if err := h.Store.Invitations.MarkAccepted(r.Context(), q, inv.TenantID, inv.ID, time.Now()); err != nil {
			return err
		}
		t, err := h.Store.Tenants.GetByID(r.Context(), q, inv.TenantID)
		if err != nil {
			return err
		}
		acceptedTenant = t
		return nil
	})
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Could not accept invitation: "+err.Error())
		return
	}
	http.Redirect(w, r, "/t/"+acceptedTenant.Slug, http.StatusFound)
}
