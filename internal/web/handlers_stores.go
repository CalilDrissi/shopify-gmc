package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/store"
)

// ----------------------------------------------------------------------------
// List + detail
// ----------------------------------------------------------------------------

func (h *Handlers) StoresList(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	d.Title = "Stores"

	q, _ := QuerierFromContext(r.Context())
	stores, err := h.Store.Stores.ListByTenant(r.Context(), q, d.Tenant.ID)
	if err != nil {
		h.Logger.Error("list stores", slog.Any("err", err))
	}
	limit, current, atLimit, _ := CheckStoreLimit(r.Context(), h.Pool, d.Tenant.ID, string(d.Tenant.Plan))
	d.Data = map[string]any{
		"Stores":   stores,
		"Limit":    limit,
		"Current":  current,
		"AtLimit":  atLimit,
		"PlanName": d.Tenant.Plan,
	}
	h.renderTenant(w, r, "stores", d)
}

func (h *Handlers) StoreDetail(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Store not found.")
		return
	}
	q, _ := QuerierFromContext(r.Context())
	s, err := h.Store.Stores.GetByID(r.Context(), q, d.Tenant.ID, id)
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Store not found.")
		return
	}
	mon := h.loadMonitoring(r, d.Tenant.ID, s.ID, string(d.Tenant.Plan),
		s.MonitorEnabled, s.MonitorFrequency, s.NextAuditAt, s.LastAuditAt, d.User.ID)
	gmcCard := h.loadGMCCard(r.Context(), d.Tenant.ID, s.ID, string(d.Tenant.Plan))
	d.Title = s.ShopDomain
	d.Data = map[string]any{
		"Store":      s,
		"Monitoring": mon,
		"GMC":        gmcCard,
		"GMCMessage": gmcMessageFor(r.URL.Query().Get("gmc")),
	}
	h.renderTenant(w, r, "store-detail", d)
}

// gmcCardView is the slim view of the GMC connection state for the
// store-detail panel. Nil means "Google integration not configured at all".
type gmcCardView struct {
	Configured     bool   // server has GOOGLE_OAUTH_CLIENT_ID etc set
	PlanAllows     bool
	PlanLabel      string
	HasConnection  bool
	StatusPill     string // "Connected" | "Warning" | "Suspended" | "Expired"
	StatusVariant  string // "success" | "warning" | "critical" | "outlined"
	MerchantID     string
	AccountEmail   string
	LastSyncAt     *time.Time
	WarningsCount  int
	SuspensionsCount int
	WebsiteClaimed *bool
}

func (h *Handlers) loadGMCCard(ctx context.Context, tenantID, storeID uuid.UUID, plan string) *gmcCardView {
	view := &gmcCardView{
		Configured: h.GMCOAuth != nil,
		PlanLabel:  GMCFor(plan).Label,
		PlanAllows: GMCFor(plan).Allowed,
	}
	if !view.Configured || h.GMC == nil {
		return view
	}
	conn, err := h.GMC.GetByStore(ctx, tenantID, storeID)
	if err != nil {
		return view
	}
	view.HasConnection = true
	view.MerchantID = conn.MerchantID
	view.AccountEmail = conn.AccountEmail
	view.LastSyncAt = conn.LastSyncAt
	view.WarningsCount = conn.Warnings
	view.SuspensionsCount = conn.Suspensions
	view.WebsiteClaimed = conn.WebsiteClaimed

	switch {
	case conn.Status == "revoked":
		view.StatusPill = "Disconnected"
		view.StatusVariant = "outlined"
	case conn.Status == "expired":
		view.StatusPill = "Expired"
		view.StatusVariant = "warning"
	case conn.Status == "error":
		view.StatusPill = "Error"
		view.StatusVariant = "critical"
	case conn.AccountStatus != nil && *conn.AccountStatus == "suspended":
		view.StatusPill = "Suspended"
		view.StatusVariant = "critical"
	case conn.AccountStatus != nil && *conn.AccountStatus == "warning":
		view.StatusPill = "Warning"
		view.StatusVariant = "warning"
	default:
		view.StatusPill = "Connected"
		view.StatusVariant = "success"
	}
	return view
}

// gmcMessageFor decodes the ?gmc= query string set after a redirect from
// Connect/Callback/Disconnect, so the store page can show a one-shot banner.
func gmcMessageFor(code string) *bannerVars {
	switch code {
	case "connected":
		return &bannerVars{Variant: "success", Title: "Google Merchant Center connected", Message: "We'll start pulling account status on your next audit."}
	case "disconnected":
		return &bannerVars{Variant: "info", Title: "Disconnected", Message: "GMC data won't appear in future audits until you reconnect."}
	case "plan":
		return &bannerVars{Variant: "warning", Title: "Plan upgrade required", Message: "Connecting Google Merchant Center is included on Starter and above."}
	}
	return nil
}

// ----------------------------------------------------------------------------
// New (form + create)
// ----------------------------------------------------------------------------

func (h *Handlers) StoreNewForm(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	limit, current, atLimit, _ := CheckStoreLimit(r.Context(), h.Pool, d.Tenant.ID, string(d.Tenant.Plan))
	if atLimit {
		h.renderPlanLimit(w, r, d, limit, current)
		return
	}
	d.Title = "Add a store"
	d.Data = map[string]any{
		"DomainField": formField{
			Label: "Shopify domain",
			Hint:  "e.g. acme-electronics.myshopify.com or your custom domain.",
			Input: inputField{Type: "text", Name: "shop_domain", ID: "shop-domain", Required: true, Placeholder: "acme.myshopify.com"},
		},
		"DisplayField": formField{
			Label: "Display name",
			Input: inputField{Type: "text", Name: "display_name", ID: "display-name", Placeholder: "Acme Electronics"},
		},
	}
	h.renderTenant(w, r, "store-new", d)
}

func (h *Handlers) StoreCreate(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if e := h.EnforcePlanLimit(r.Context(), d.Tenant.ID, string(d.Tenant.Plan), ResStores); e != nil {
		h.RenderPlanLimit(w, r, e)
		return
	}

	domainInput := strings.TrimSpace(r.FormValue("shop_domain"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	domain, err := normalizeShopifyDomain(domainInput)
	if err != nil {
		h.renderStoreNewWithError(w, r, d, domainInput, displayName, err.Error())
		return
	}

	if err := pingURL(r.Context(), StoreURLFor(domain), 8*time.Second); err != nil {
		h.renderStoreNewWithError(w, r, d, domainInput, displayName,
			"Couldn't reach "+StoreURLFor(domain)+". Check the spelling and try again. ("+err.Error()+")")
		return
	}

	q, _ := QuerierFromContext(r.Context())
	s := &store.Shop{ShopDomain: domain}
	if displayName != "" {
		s.DisplayName = &displayName
	}
	if err := h.Store.Stores.Insert(r.Context(), q, d.Tenant.ID, s); err != nil {
		if isUniqueViolation(err, "shop_domain") {
			h.renderStoreNewWithError(w, r, d, domainInput, displayName, "This store is already connected.")
			return
		}
		h.Logger.Error("insert store", slog.Any("err", err))
		h.renderError(w, http.StatusInternalServerError, "Could not save the store.")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/t/%s/stores/%s", d.Tenant.Slug, s.ID), http.StatusFound)
}

func (h *Handlers) renderStoreNewWithError(w http.ResponseWriter, r *http.Request, d tenantPageData, domain, displayName, msg string) {
	d.Title = "Add a store"
	d.Flash = &bannerVars{Variant: "critical", Title: "Couldn't add the store", Message: msg}
	d.Data = map[string]any{
		"DomainField": formField{
			Label: "Shopify domain",
			Error: msg,
			Input: inputField{Type: "text", Name: "shop_domain", ID: "shop-domain", Required: true, Value: domain, Invalid: true},
		},
		"DisplayField": formField{
			Label: "Display name",
			Input: inputField{Type: "text", Name: "display_name", ID: "display-name", Value: displayName},
		},
	}
	h.renderTenant(w, r, "store-new", d)
}

func (h *Handlers) renderPlanLimit(w http.ResponseWriter, r *http.Request, d tenantPageData, limit, current int) {
	w.WriteHeader(http.StatusPaymentRequired)
	d.Title = "Plan limit reached"
	d.Data = map[string]any{
		"Limit":    limit,
		"Current":  current,
		"PlanName": d.Tenant.Plan,
	}
	h.renderTenant(w, r, "plan-limit", d)
}

// ----------------------------------------------------------------------------
// Delete
// ----------------------------------------------------------------------------

func (h *Handlers) StoreDelete(w http.ResponseWriter, r *http.Request) {
	d := h.buildTenantData(r)
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	q, _ := QuerierFromContext(r.Context())
	if _, err := q.Exec(r.Context(),
		`DELETE FROM stores WHERE tenant_id=$1 AND id=$2`, d.Tenant.ID, id,
	); err != nil {
		h.Logger.Error("delete store", slog.Any("err", err))
	}
	http.Redirect(w, r, fmt.Sprintf("/t/%s/stores", d.Tenant.Slug), http.StatusFound)
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// normalizeShopifyDomain accepts user-provided values like "acme",
// "acme.myshopify.com", or "https://acme.myshopify.com/" and returns a
// hostname suitable for storage.
func normalizeShopifyDomain(in string) (string, error) {
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "" {
		return "", errors.New("domain is required")
	}
	if !strings.Contains(in, "://") {
		in = "https://" + in
	}
	u, err := url.Parse(in)
	if err != nil || u.Host == "" {
		return "", errors.New("not a valid URL or domain")
	}
	host := strings.TrimPrefix(u.Host, "www.")
	hasPort := strings.Contains(host, ":")
	hasDot := strings.Contains(host, ".")
	// If the user typed a bare handle ("acme") with no dot and no port,
	// resolve it to the canonical .myshopify.com subdomain.
	if !hasDot && !hasPort {
		host += ".myshopify.com"
	}
	return host, nil
}

// pingURL sends a HEAD then GET to confirm the store is reachable.
// Accepts either https://… or http://… URLs (the latter only used by the dev
// fixture flows; production stores always resolve to https).
func pingURL(ctx context.Context, target string, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	tryOne := func(u string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "ShopifyGMCBot/1.0 (verifying store URL)")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return fmt.Errorf("server returned %d", resp.StatusCode)
		}
		return nil
	}
	if err := tryOne(target); err == nil {
		return nil
	} else if isLocalhostURL(target) {
		// Dev convenience: stores pointing at localhost frequently aren't on TLS.
		alt := strings.Replace(target, "https://", "http://", 1)
		return tryOne(alt)
	} else {
		return err
	}
}

// StoreURLFor turns a stored shop_domain into a fully-qualified URL the
// crawler can hit. Defaults to https://; falls back to http:// for the
// localhost/127.* dev paths so test fixtures work without TLS.
func StoreURLFor(domain string) string {
	if isLocalhost(domain) {
		return "http://" + domain
	}
	return "https://" + domain
}

func isLocalhost(domain string) bool {
	return strings.HasPrefix(domain, "localhost") || strings.HasPrefix(domain, "127.")
}

func isLocalhostURL(u string) bool {
	return strings.Contains(u, "://localhost") || strings.Contains(u, "://127.")
}
