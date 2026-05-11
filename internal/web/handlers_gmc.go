package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/example/gmcauditor/internal/auth"
	"github.com/example/gmcauditor/internal/gmc"
)

// sessionCookieName aliases auth.SessionCookieName so the GMC handlers don't
// have to import auth themselves (keeps the local read concise).
var sessionCookieName = auth.SessionCookieName

// GMCConnect kicks off the OAuth flow. Owner-only (mounted under ownerMW),
// blocked while impersonating, and the connect link's per-store path is
// re-derived on the server — we never trust client-supplied tenant/store
// values, only the path-vars resolved by the tenant middleware.
func (h *Handlers) GMCConnect(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	if d.Impersonating {
		h.renderError(w, http.StatusForbidden, "Platform admins can't initiate OAuth connections while impersonating.")
		return
	}
	if !GMCFor(string(d.Tenant.Plan)).Allowed {
		http.Redirect(w, r, fmt.Sprintf("/t/%s/stores/%s?gmc=plan", d.Tenant.Slug, r.PathValue("id")), http.StatusFound)
		return
	}
	if e := h.EnforcePlanLimit(r.Context(), d.Tenant.ID, string(d.Tenant.Plan), ResGMCConnections); e != nil {
		h.RenderPlanLimit(w, r, e)
		return
	}
	storeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Store not found.")
		return
	}

	// Use the request's session id as the CSRF anchor — it's already a
	// random opaque token in the auth.Session table, so binding the
	// state to it ensures the callback can only succeed in the originating
	// browser.
	sessID := h.sessionIDFromRequest(r)
	if sessID == "" {
		h.renderError(w, http.StatusUnauthorized, "Sign in first.")
		return
	}
	state := gmc.SignOAuthState(h.AppSecret, gmc.OAuthState{
		SessionID: sessID,
		TenantID:  d.Tenant.ID,
		StoreID:   storeID,
		UserID:    d.User.ID,
	})
	http.Redirect(w, r, h.GMCOAuth.AuthCodeURL(state), http.StatusFound)
}

// GMCCallback runs at the fixed /oauth/google/callback path. We deliberately
// don't put it under tenantMW — the user might be redirected back through
// any subdomain, and the state token carries everything we need.
func (h *Handlers) GMCCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if errParam := q.Get("error"); errParam != "" {
		h.renderError(w, http.StatusBadRequest,
			fmt.Sprintf("Google denied the connection: %s. You can try again from the store page.", errParam))
		return
	}
	code := q.Get("code")
	stateRaw := q.Get("state")
	if code == "" || stateRaw == "" {
		h.renderError(w, http.StatusBadRequest, "Missing code or state — try connecting from the store page again.")
		return
	}

	st, err := gmc.VerifyOAuthState(h.AppSecret, stateRaw)
	if err != nil {
		h.Logger.Warn("gmc_state_invalid", slog.Any("err", err))
		h.renderError(w, http.StatusBadRequest, "This Google sign-in link expired or was tampered with. Please retry.")
		return
	}

	// Bind to the current session — defends against an attacker tricking
	// a second user into clicking a link with someone else's state.
	currentSess := h.sessionIDFromRequest(r)
	if currentSess == "" || currentSess != st.SessionID {
		h.renderError(w, http.StatusForbidden,
			"That sign-in came back to a different session. Sign in as the same user that started the connect, then retry.")
		return
	}

	tok, err := h.GMCOAuth.Exchange(r.Context(), code)
	if err != nil {
		h.Logger.Warn("gmc_exchange_failed", slog.Any("err", err))
		h.renderError(w, http.StatusBadGateway, "Could not exchange the Google authorization code. Try again.")
		return
	}
	if tok.RefreshToken == "" {
		// Almost always means prompt=consent wasn't honoured — the user has
		// previously consented and Google won't re-issue the refresh. We have
		// to redirect them to revoke our app at https://myaccount.google.com/permissions
		// or call accounts.revoke ourselves to force a re-consent.
		h.renderError(w, http.StatusBadRequest,
			"Google didn't return a refresh token. Open https://myaccount.google.com/permissions, remove shopifygmc, then retry the connect.")
		return
	}

	// One-shot ListAccounts so we can auto-link the merchant or render a
	// picker. We construct a one-off TokenSupplier that just returns the
	// freshly-minted access token rather than going through ConnectionStore
	// (which doesn't have a row to look up yet).
	once := tok.AccessToken
	cli := gmc.NewClient(func(_ context.Context) (string, error) { return once, nil }, h.Logger)
	if h.GMCBaseURL != "" {
		cli.BaseURL = h.GMCBaseURL
	}
	accts, err := cli.ListAccounts(r.Context())
	if err != nil {
		h.Logger.Warn("gmc_list_accounts_failed", slog.Any("err", err))
		h.renderError(w, http.StatusBadGateway, "Connected to Google, but couldn't list your Merchant Center accounts.")
		return
	}
	if len(accts) == 0 {
		h.renderError(w, http.StatusBadRequest,
			"Your Google account isn't linked to any Merchant Center account. Visit merchants.google.com to set one up, then retry.")
		return
	}

	// Look up the tenant slug for the redirect (state has the ID, not the slug).
	var slug string
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT slug FROM tenants WHERE id=$1`, st.TenantID,
	).Scan(&slug); err != nil {
		h.renderError(w, http.StatusNotFound, "Workspace not found.")
		return
	}

	if len(accts) == 1 {
		// Auto-link.
		if _, err := h.GMC.Upsert(r.Context(), st.TenantID, st.StoreID, accts[0].ID, "", tok); err != nil {
			h.Logger.Error("gmc_upsert", slog.Any("err", err))
			h.renderError(w, http.StatusInternalServerError, "Saved Google sign-in, but couldn't store the connection.")
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/t/%s/stores/%s?gmc=connected", slug, st.StoreID), http.StatusFound)
		return
	}

	// Multiple merchant accounts — render a picker. We stash the freshly-
	// issued refresh token in a tx-bound short-lived row keyed by state, so
	// the picker form's POST can finish the upsert without re-running OAuth.
	pickToken := gmc.SignOAuthState(h.AppSecret, gmc.OAuthState{
		SessionID: st.SessionID,
		TenantID:  st.TenantID,
		StoreID:   st.StoreID,
		UserID:    st.UserID,
		Nonce:     tok.RefreshToken, // hidden in the signed payload only
	})
	h.render(w, r, "gmc-picker", map[string]any{
		"Title":     "Choose Merchant Center account",
		"Tenant":    map[string]any{"Slug": slug},
		"StoreID":   st.StoreID,
		"Accounts":  accts,
		"PickToken": pickToken,
	})
}

// GMCPickerSubmit completes the connect when the user had multiple
// Merchant Center accounts. The POST carries the merchant_id they chose
// plus the signed pick-token whose nonce field hides the refresh token.
func (h *Handlers) GMCPickerSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	pickToken := r.FormValue("token")
	merchantID := r.FormValue("merchant_id")
	if pickToken == "" || merchantID == "" {
		h.renderError(w, http.StatusBadRequest, "Missing fields.")
		return
	}
	st, err := gmc.VerifyOAuthState(h.AppSecret, pickToken)
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "This selection link expired. Retry the connect.")
		return
	}
	if h.sessionIDFromRequest(r) != st.SessionID {
		h.renderError(w, http.StatusForbidden, "Session mismatch.")
		return
	}
	refreshToken := st.Nonce // we put it there in GMCCallback
	tok, err := h.GMC.OAuth.Refresh(r.Context(), refreshToken)
	if err != nil {
		h.renderError(w, http.StatusBadGateway, "Couldn't finalise the connection.")
		return
	}
	tok.RefreshToken = refreshToken // Refresh() returns "" — we still want to persist
	if _, err := h.GMC.Upsert(r.Context(), st.TenantID, st.StoreID, merchantID, "", tok); err != nil {
		h.Logger.Error("gmc_upsert", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Couldn't save the connection.")
		return
	}
	var slug string
	_ = h.Pool.QueryRow(r.Context(), `SELECT slug FROM tenants WHERE id=$1`, st.TenantID).Scan(&slug)
	http.Redirect(w, r, fmt.Sprintf("/t/%s/stores/%s?gmc=connected", slug, st.StoreID), http.StatusFound)
}

// GMCDisconnect revokes the refresh token at Google then clears the local
// row. Best-effort revoke — if Google rejects we still wipe locally.
func (h *Handlers) GMCDisconnect(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	if d.Impersonating {
		h.renderError(w, http.StatusForbidden, "Platform admins can't disconnect while impersonating.")
		return
	}
	storeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Store not found.")
		return
	}
	conn, err := h.GMC.GetByStore(r.Context(), d.Tenant.ID, storeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Redirect(w, r, fmt.Sprintf("/t/%s/stores/%s", d.Tenant.Slug, storeID), http.StatusFound)
			return
		}
		h.renderError(w, http.StatusInternalServerError, "Could not load connection.")
		return
	}

	// Decrypt once, revoke at Google, then mark revoked locally.
	var enc []byte
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT refresh_token_encrypted FROM store_gmc_connections WHERE id=$1`, conn.ID,
	).Scan(&enc)
	if plain, decErr := h.GMC.Cipher.Decrypt(enc); decErr == nil && len(plain) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		_ = h.GMC.OAuth.Revoke(ctx, string(plain))
		cancel()
		for i := range plain {
			plain[i] = 0
		}
	}
	if err := h.GMC.MarkRevoked(r.Context(), conn.ID, "user disconnect"); err != nil {
		h.Logger.Error("gmc_disconnect", slog.Any("err", err))
	}
	http.Redirect(w, r, fmt.Sprintf("/t/%s/stores/%s?gmc=disconnected", d.Tenant.Slug, storeID), http.StatusFound)
}

// sessionIDFromRequest reads the auth.SessionCookie and returns the
// session ID as a string, or "" if no valid cookie. Reused by Connect +
// Callback to bind state to a session.
func (h *Handlers) sessionIDFromRequest(r *http.Request) string {
	cv, err := h.Cookies.Read(r, sessionCookieName)
	if err != nil {
		return ""
	}
	return cv.SessionID.String()
}
