package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/auth"
	"github.com/example/gmcauditor/internal/billing"
	"github.com/example/gmcauditor/internal/gmc"
	"github.com/example/gmcauditor/internal/mailer"
	"github.com/example/gmcauditor/internal/settings"
	"github.com/example/gmcauditor/internal/store"
	"github.com/example/gmcauditor/internal/web"
)

func main() {
	addr := ":8080"
	if v := os.Getenv("APP_PORT"); v != "" {
		addr = ":" + v
	}
	dbURL := getenv("DATABASE_URL", "postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable")
	baseURL := getenv("APP_BASE_URL", "http://localhost:8080")
	appSecret := getenv("APP_SECRET", "gmcauditor-dev-secret-not-for-prod")
	smtpHost := getenv("SMTP_HOST", "localhost")
	smtpPort := getenv("SMTP_PORT", "1025")
	mailFrom := getenv("SMTP_FROM", "noreply@gmcauditor.local")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	st := store.NewStore(pool)

	sqlDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("sql.Open: %v", err)
	}
	defer sqlDB.Close()
	sessions := auth.NewSessionStore(auth.NewPostgresSessionDB(sqlDB), 7*24*time.Hour)

	hashKey, blockKey := cookieKeys(appSecret)
	cookies := auth.NewCookieManager(hashKey, blockKey, false)

	mail := mailer.NewSMTPMailer(mailer.SMTPConfig{Host: smtpHost, Port: smtpPort, From: mailFrom}, logger)

	rend := web.NewRenderer(template.FuncMap{
		"mergeInputInvalid": func(in InputData, invalid bool) InputData {
			in.Invalid = invalid
			return in
		},
		// dict builds a string→any map from "key", value pairs — used by partials
		// that need to be invoked with a synthetic context.
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict needs an even number of args")
			}
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				k, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key must be a string, got %T", values[i])
				}
				m[k] = values[i+1]
			}
			return m, nil
		},
		// deref returns "" for a nil *string and the underlying string otherwise.
		"deref": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
		"derefInt": func(i *int) int {
			if i == nil {
				return 0
			}
			return *i
		},
		"deref_bool": func(b *bool) bool {
			return b != nil && *b
		},
		// toFloat converts an int to a float64 so price templates can do
		// "{{ printf "%.0f" (toFloat .PriceCents) }}" to render dollars.
		"toFloat": func(i int) float64 { return float64(i) / 100 },
		"upper":   func(s string) string { return strings.ToUpper(s) },
		"trim":    func(s string) string { return strings.TrimSpace(s) },
		// Tiny arithmetic helpers used by the score gauge.
		"sub":   func(a, b int) int { return a - b },
		"mul":   func(a int, b float64) float64 { return float64(a) * b },
		"mulf":  func(a, b float64) float64 { return a * b },
		"intDiv": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a / b
		},
		// humanBytes renders 1024-base sizes for the /admin/mail page.
		// Picks the largest unit where the value is >= 1; one decimal except
		// for bytes. 0 → "0 B" (cleaner than "0.0 B").
		"humanBytes": func(n int64) string {
			if n <= 0 {
				return "0 B"
			}
			const k = 1024
			if n < k {
				return fmt.Sprintf("%d B", n)
			}
			div, exp := int64(k), 0
			for x := n / k; x >= k; x /= k {
				div *= k
				exp++
			}
			units := []string{"KB", "MB", "GB", "TB", "PB"}
			if exp >= len(units) {
				exp = len(units) - 1
			}
			return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
		},
		// pctUsed = used*100/quota, clamped to [0,100]. Returns 0 when quota
		// is 0 (unlimited) so the >=90% red-cell check is correctly false.
		"pctUsed": func(used, quota int64) int {
			if quota <= 0 || used <= 0 {
				return 0
			}
			p := used * 100 / quota
			if p > 100 {
				return 100
			}
			return int(p)
		},
	})
	if err := rend.LoadPartials("templates/partials/*.html"); err != nil {
		log.Fatalf("load partials: %v", err)
	}
	pages := []web.PageDef{
		{Name: "landing", Layout: "templates/layouts/public.html", Template: "templates/pages/landing.html"},
		{Name: "pricing", Layout: "templates/layouts/public.html", Template: "templates/pages/pricing.html"},
		{Name: "features", Layout: "templates/layouts/public.html", Template: "templates/pages/features.html"},
		{Name: "signup", Layout: "templates/layouts/public.html", Template: "templates/pages/signup.html"},
		{Name: "login", Layout: "templates/layouts/public.html", Template: "templates/pages/login.html"},
		{Name: "verify-email", Layout: "templates/layouts/public.html", Template: "templates/pages/verify-email.html"},
		{Name: "verify-email-pending", Layout: "templates/layouts/public.html", Template: "templates/pages/verify-email-pending.html"},
		{Name: "forgot-password", Layout: "templates/layouts/public.html", Template: "templates/pages/forgot-password.html"},
		{Name: "reset-password", Layout: "templates/layouts/public.html", Template: "templates/pages/reset-password.html"},
		{Name: "invitation", Layout: "templates/layouts/public.html", Template: "templates/pages/invitation.html"},
		{Name: "error", Layout: "templates/layouts/public.html", Template: "templates/pages/error.html"},
		{Name: "account", Layout: "templates/layouts/public.html", Template: "templates/pages/account.html"},
		{Name: "dashboard", Layout: "templates/layouts/tenant.html", Template: "templates/pages/dashboard.html"},
		{Name: "members", Layout: "templates/layouts/tenant.html", Template: "templates/pages/members.html"},
		{Name: "stores", Layout: "templates/layouts/tenant.html", Template: "templates/pages/stores.html"},
		{Name: "store-new", Layout: "templates/layouts/tenant.html", Template: "templates/pages/store-new.html"},
		{Name: "store-detail", Layout: "templates/layouts/tenant.html", Template: "templates/pages/store-detail.html"},
		{Name: "plan-limit", Layout: "templates/layouts/tenant.html", Template: "templates/pages/plan-limit.html"},
		{Name: "audit-detail", Layout: "templates/layouts/tenant.html", Template: "templates/pages/audit-detail.html"},
		{Name: "audit-report-pdf", Layout: "templates/layouts/pdf.html", Template: "templates/pages/audit-report-pdf.html"},
		{Name: "audits-list", Layout: "templates/layouts/tenant.html", Template: "templates/pages/audits-list.html"},
		{Name: "unsubscribe", Layout: "templates/layouts/public.html", Template: "templates/pages/unsubscribe.html"},
		{Name: "gmc-picker", Layout: "templates/layouts/public.html", Template: "templates/pages/gmc-picker.html"},
		{Name: "billing", Layout: "templates/layouts/tenant.html", Template: "templates/pages/billing.html"},
		{Name: "admin-login", Layout: "templates/layouts/public.html", Template: "templates/pages/admin-login.html"},
		{Name: "admin-dashboard", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-dashboard.html"},
		{Name: "admin-tenants", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-tenants.html"},
		{Name: "admin-tenant-detail", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-tenant-detail.html"},
		{Name: "admin-audit-log", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-audit-log.html"},
		{Name: "admin-settings", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-settings.html"},
		{Name: "admin-gmc", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-gmc.html"},
		{Name: "admin-mail", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-mail.html"},
		{Name: "admin-mail-import", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-mail-import.html"},
		{Name: "admin-mail-import-result", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-mail-import-result.html"},
		{Name: "admin-mail-activity", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-mail-activity.html"},
		{Name: "admin-mail-vacation", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-mail-vacation.html"},
		{Name: "admin-mail-filters", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-mail-filters.html"},
		{Name: "admin-mail-spam", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-mail-spam.html"},
	}
	if err := rend.Register(pages); err != nil {
		log.Fatalf("register pages: %v", err)
	}

	// chromedp: use Playwright's bundled Chromium when no system chromium
	// is available (dev container). Falls back to PATH lookup.
	if p := findChromium(); p != "" {
		web.SetPDFChromePath(p)
		logger.Info("pdf chromium", slog.String("path", p))
	}

	// settings cipher is needed both by the admin/settings page and by
	// the GMC connection store (which uses it to wrap refresh tokens).
	// Initialised here so the GMC wiring below can reference it.
	settingsCipher, settingsErr := settings.NewCipherFromEnv()
	if settingsErr != nil {
		ephemeral := make([]byte, 32)
		_, _ = rand.Read(ephemeral)
		settingsCipher, _ = settings.NewCipher(base64.StdEncoding.EncodeToString(ephemeral))
		logger.Warn("settings: SETTINGS_ENCRYPTION_KEY not set, using ephemeral key for this run")
	}

	// GMC OAuth + connection store wiring. The handlers tolerate nil
	// fields, so installs without Google OAuth credentials can still run
	// without the connect button breaking.
	var (
		gmcOAuth *gmc.OAuth
		gmcConns *gmc.ConnectionStore
	)
	gmcClientID := os.Getenv("GOOGLE_OAUTH_CLIENT_ID")
	gmcClientSecret := os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET")
	if gmcClientID != "" && gmcClientSecret != "" {
		redirect := getenv("GOOGLE_OAUTH_REDIRECT_URL", baseURL+"/oauth/google/callback")
		gmcOAuth = &gmc.OAuth{
			ClientID:     gmcClientID,
			ClientSecret: gmcClientSecret,
			RedirectURL:  redirect,
			AuthURL:      os.Getenv("GOOGLE_OAUTH_AUTH_URL"),   // optional override
			TokenURL:     os.Getenv("GOOGLE_OAUTH_TOKEN_URL"),  // optional override
			RevokeURL:    os.Getenv("GOOGLE_OAUTH_REVOKE_URL"), // optional override
		}
		gmcConns = gmc.NewConnectionStore(pool, settingsCipher, gmcOAuth)
		logger.Info("gmc_oauth_configured", slog.String("redirect_url", redirect))
	} else {
		logger.Info("gmc_oauth_disabled", slog.String("reason", "GOOGLE_OAUTH_CLIENT_ID/SECRET not set"))
	}

	// Gumroad billing wiring. Catalog + dispatcher are constructed even
	// when the secret/products aren't set so the pricing/billing pages
	// can render placeholder buttons.
	gumroadCatalog := billing.LoadCatalog()
	gumroadDispatcher := &billing.Dispatcher{
		Pool:          pool,
		Catalog:       gumroadCatalog,
		Logger:        logger,
		Mail:          mail,
		MailFrom:      mailFrom,
		OperatorEmail: getenv("OPERATOR_EMAIL", mailFrom),
	}
	gumroadSecret := []byte(os.Getenv("GUMROAD_WEBHOOK_SECRET"))
	if len(gumroadSecret) == 0 {
		logger.Warn("gumroad_webhook_secret_unset", slog.String("hint", "set GUMROAD_WEBHOOK_SECRET to verify Gumroad pings"))
	}

	csrfMgr := auth.NewCSRFManager(hashKey)
	h := &web.Handlers{
		Pool: pool, Store: st, Renderer: rend,
		Cookies: cookies, Sessions: sessions, CSRF: csrfMgr,
		Mailer: mail, BaseURL: baseURL, MailFrom: mailFrom,
		AppSecret:  []byte(appSecret),
		LoginLimit: web.NewLoginLimiter(),
		Logger:     logger,
		GMC:        gmcConns,
		GMCOAuth:   gmcOAuth,
		GMCBaseURL: os.Getenv("GMC_BASE_URL"),
		Gumroad:        gumroadDispatcher,
		GumroadCatalog: gumroadCatalog,
		GumroadSecret:  gumroadSecret,
	}

	mux := http.NewServeMux()

	// Liveness / readiness — unauthenticated, fast, no DB write.
	// /healthz: process is alive. /readyz: process can talk to Postgres.
	// Probes are typically run by a load balancer or orchestrator; we
	// short-circuit them out of the slog request logger to avoid drowning
	// the audit trail (the RequestLogger middleware skips paths starting
	// with /healthz or /readyz).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db unreachable"))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ready"))
	})

	// Static + components demo (carry-over from prompt 7).
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("GET /_components", componentsDemo)

	// Public routes.
	mux.HandleFunc("GET /{$}", h.Landing)
	mux.HandleFunc("GET /pricing", h.PricingPage)
	mux.HandleFunc("POST /webhooks/gumroad", h.GumroadWebhook)
	mux.HandleFunc("GET /features", h.Features)
	mux.HandleFunc("GET /signup", h.SignupForm)
	mux.HandleFunc("POST /signup", h.Signup)
	mux.HandleFunc("GET /login", h.LoginForm)
	mux.HandleFunc("POST /login", h.Login)
	mux.HandleFunc("POST /logout", h.Logout)
	mux.HandleFunc("GET /verify-email/{token}", h.VerifyEmail)
	mux.HandleFunc("POST /verify-email/resend", h.ResendVerification)
	mux.HandleFunc("GET /verify-email-pending", h.VerifyEmailPending)
	// favicon + robots — both are universal browser/crawler requests; without
	// them every landing-page load logs a console 404 and search-engine
	// crawlers see a 404 trying to find robots.txt.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/favicon.ico")
	})
	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, "static/favicon.svg")
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /admin/\nDisallow: /t/\nAllow: /\n"))
	})
	mux.HandleFunc("GET /forgot-password", h.ForgotForm)
	mux.HandleFunc("POST /forgot-password", h.Forgot)
	mux.HandleFunc("GET /reset-password/{token}", h.ResetForm)
	mux.HandleFunc("POST /reset-password/{token}", h.Reset)
	mux.HandleFunc("GET /invitations/{token}", h.Invitation)
	mux.HandleFunc("POST /invitations/{token}/accept", h.AcceptInvitation)
	mux.HandleFunc("GET /unsubscribe/{token}", h.Unsubscribe)

	mux.HandleFunc("GET /account", h.AccountPage)
	mux.HandleFunc("POST /account/switch/{tenant_id}", h.SwitchTenant)

	// Tenant routes — full middleware chain:
	//   RequireUser → LoadTenantBySlug → RequireMembership →
	//   CheckImpersonation → SetRLSContext → CSRF [→ RequireOwner on mutations]
	notFound := func(w http.ResponseWriter, r *http.Request) { h.NotFound(w, r) }
	forbidden := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		t, _ := h.Renderer.Page("error")
		_ = t.ExecuteTemplate(w, "layout-public", map[string]any{"Title": "Forbidden", "Status": 403, "Message": "You can't access this workspace."})
	}
	users := web.NewUserLookup(pool, st)
	tenantMW := web.Chain(
		web.RequireUser(cookies, sessions, users),
		web.LoadTenantBySlug(pool, st, notFound),
		web.RequireMembership(pool, st, forbidden),
		web.CheckImpersonation,
		web.SetRLSContext(pool),
		web.CSRF(csrfMgr),
	)
	ownerMW := web.Chain(tenantMW, web.RequireOwner(forbidden))

	mux.Handle("GET /t/{slug}", tenantMW(http.HandlerFunc(h.Dashboard)))
	mux.Handle("GET /t/{slug}/billing", ownerMW(http.HandlerFunc(h.BillingPage)))
	mux.Handle("GET /t/{slug}/billing/poll", tenantMW(http.HandlerFunc(h.BillingPollFragment)))
	mux.Handle("GET /t/{slug}/members", tenantMW(http.HandlerFunc(h.MembersPage)))
	mux.Handle("POST /t/{slug}/invitations", ownerMW(http.HandlerFunc(h.CreateInvitation)))
	mux.Handle("POST /t/{slug}/invitations/{id}/revoke", ownerMW(http.HandlerFunc(h.RevokeInvitation)))
	mux.Handle("POST /t/{slug}/memberships/{user_id}/remove", ownerMW(http.HandlerFunc(h.RemoveMembership)))
	mux.Handle("POST /t/{slug}/transfer-ownership", ownerMW(http.HandlerFunc(h.TransferOwnership)))
	mux.Handle("POST /t/{slug}/delete", ownerMW(http.HandlerFunc(h.DeleteTenant)))

	// Stores CRUD
	mux.Handle("GET /t/{slug}/stores", tenantMW(http.HandlerFunc(h.StoresList)))
	mux.Handle("GET /t/{slug}/stores/new", ownerMW(http.HandlerFunc(h.StoreNewForm)))
	mux.Handle("POST /t/{slug}/stores/new", ownerMW(http.HandlerFunc(h.StoreCreate)))
	mux.Handle("GET /t/{slug}/stores/{id}", tenantMW(http.HandlerFunc(h.StoreDetail)))
	mux.Handle("POST /t/{slug}/stores/{id}/delete", ownerMW(http.HandlerFunc(h.StoreDelete)))
	mux.Handle("POST /t/{slug}/stores/{id}/monitoring", tenantMW(http.HandlerFunc(h.MonitoringUpdate)))
	mux.Handle("POST /t/{slug}/stores/{id}/run-now", tenantMW(http.HandlerFunc(h.RunNow)))
	mux.Handle("POST /t/{slug}/stores/{id}/subscriptions", tenantMW(http.HandlerFunc(h.SubscriptionUpdate)))

	// GMC OAuth routes.
	// Connect/disconnect are scoped to a specific store + require owner;
	// the callback is fixed (no per-store path), the picker submit also
	// public (state-token-bound).
	mux.Handle("GET /t/{slug}/stores/{id}/gmc/connect", ownerMW(http.HandlerFunc(h.GMCConnect)))
	mux.Handle("POST /t/{slug}/stores/{id}/gmc/disconnect", ownerMW(http.HandlerFunc(h.GMCDisconnect)))
	mux.HandleFunc("GET /oauth/google/callback", h.GMCCallback)
	mux.HandleFunc("POST /oauth/google/select", h.GMCPickerSubmit)

	// Audits
	mux.Handle("POST /t/{slug}/stores/{id}/audits", tenantMW(http.HandlerFunc(h.EnqueueAudit)))
	mux.Handle("GET /t/{slug}/audits", tenantMW(http.HandlerFunc(h.AuditsList)))
	mux.Handle("GET /t/{slug}/audits/{id}", tenantMW(http.HandlerFunc(h.AuditDetail)))
	mux.Handle("GET /t/{slug}/audits/{id}/status", tenantMW(http.HandlerFunc(h.AuditProgressFragment)))
	mux.Handle("GET /t/{slug}/audits/{id}/report.pdf", tenantMW(http.HandlerFunc(h.ReportPDF)))
	mux.Handle("POST /t/{slug}/audits/{id}/issues/{issue_id}/resolve", tenantMW(http.HandlerFunc(h.ResolveIssue)))

	// Platform admin routes.
	settingsBackend := settings.NewPostgresBackend(pool)
	settingsAuditor := settings.NewPostgresAuditor(pool)
	settingsSvc := settings.New(settingsCipher, settingsBackend, settingsAuditor)
	adminH := &web.AdminHandlers{
		Pool: pool, Store: st, Renderer: rend,
		Cookies: cookies, Sessions: sessions, CSRF: csrfMgr,
		Mailer: mail, Settings: settingsSvc, BaseURL: baseURL, MailFrom: mailFrom,
		Logger: logger,
	}

	mux.HandleFunc("GET /admin/login", adminH.LoginForm)
	mux.HandleFunc("POST /admin/login", adminH.Login)
	mux.HandleFunc("POST /admin/logout", adminH.Logout)
	mux.HandleFunc("GET /admin/{$}", adminH.Dashboard)
	mux.HandleFunc("GET /admin", adminH.Dashboard)
	mux.HandleFunc("GET /admin/tenants", adminH.TenantsList)
	mux.HandleFunc("GET /admin/tenants/{id}", adminH.TenantDetail)
	mux.HandleFunc("POST /admin/tenants/{id}/suspend", adminH.TenantSuspend)
	mux.HandleFunc("POST /admin/tenants/{id}/unsuspend", adminH.TenantUnsuspend)
	mux.HandleFunc("POST /admin/tenants/{id}/impersonate", adminH.ImpersonationStart)
	mux.HandleFunc("POST /admin/impersonation/stop", adminH.ImpersonationStop)
	mux.HandleFunc("GET /admin/audit-log", adminH.AuditLogPage)
	mux.HandleFunc("GET /admin/gmc", adminH.GMCPage)
	mux.HandleFunc("GET /admin/mail", adminH.MailPage)
	mux.HandleFunc("POST /admin/mail/add", adminH.MailAdd)
	mux.HandleFunc("POST /admin/mail/passwd", adminH.MailPasswd)
	mux.HandleFunc("POST /admin/mail/del", adminH.MailDel)
	mux.HandleFunc("POST /admin/mail/alias", adminH.MailAlias)
	mux.HandleFunc("POST /admin/mail/unalias", adminH.MailUnalias)
	mux.HandleFunc("POST /admin/mail/quota", adminH.MailQuota)
	mux.HandleFunc("GET /admin/mail/import", adminH.MailImportPage)
	mux.HandleFunc("POST /admin/mail/import", adminH.MailImportSubmit)
	mux.HandleFunc("GET /admin/mail/activity", adminH.MailActivity)
	mux.HandleFunc("POST /admin/mail/suspend", adminH.MailSuspend)
	mux.HandleFunc("GET /admin/mail/vacation", adminH.MailVacationGet)
	mux.HandleFunc("POST /admin/mail/vacation", adminH.MailVacationSave)
	mux.HandleFunc("GET /admin/mail/filters", adminH.MailFilters)
	mux.HandleFunc("POST /admin/mail/filters/add", adminH.MailFilterAdd)
	mux.HandleFunc("POST /admin/mail/filters/del", adminH.MailFilterDel)
	mux.HandleFunc("GET /admin/mail/spam", adminH.MailSpamGet)
	mux.HandleFunc("POST /admin/mail/spam/add", adminH.MailSpamAdd)
	mux.HandleFunc("POST /admin/mail/spam/del", adminH.MailSpamDel)
	mux.HandleFunc("POST /admin/mail/spam/threshold", adminH.MailSpamThreshold)
	mux.HandleFunc("GET /admin/settings", adminH.SettingsPage)
	mux.HandleFunc("POST /admin/settings", adminH.SettingsSave)
	mux.HandleFunc("GET /admin/settings/test-connection", adminH.SettingsTestConnection)

	// Catch-all for unmatched paths.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h.NotFound(w, r)
	})

	// Wrap with middleware: SecurityHeaders → RequestID → RequestLogger.
	chain := web.SecurityHeaders(web.SecurityHeadersOptions{Production: false})(
		web.RequestID(
			web.RequestLogger(logger)(mux),
		),
	)

	srv := &http.Server{
		Addr:              addr,
		Handler:           chain,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("listening", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Info("server_shutting_down", slog.Duration("drain_deadline", 30*time.Second))
	shutdownCtx, cancelShut := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShut()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("server_shutdown_error", slog.Any("err", err))
	} else {
		logger.Info("server_shutdown_clean")
	}
}

func cookieKeys(appSecret string) (hashKey, blockKey []byte) {
	h := sha512.Sum512([]byte("hash:" + appSecret))
	b := sha256.Sum256([]byte("block:" + appSecret))
	return h[:32], b[:]
}

// findChromium searches the user's Playwright cache for a usable Chromium.
// Returns "" if none is found, in which case chromedp falls back to PATH.
func findChromium() string {
	if v := os.Getenv("CHROMIUM_PATH"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	for _, root := range []string{
		os.ExpandEnv("$HOME/.cache/ms-playwright"),
		"/home/codespace/.cache/ms-playwright",
		"/root/.cache/ms-playwright",
	} {
		matches, _ := filepath.Glob(root + "/chromium-*/chrome-linux*/chrome")
		for _, m := range matches {
			if _, err := os.Stat(m); err == nil {
				return m
			}
		}
		matches2, _ := filepath.Glob(root + "/chromium_headless_shell-*/chrome-linux*/headless_shell")
		for _, m := range matches2 {
			if _, err := os.Stat(m); err == nil {
				return m
			}
		}
	}
	return ""
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ============================================================================
// Components demo (carry-over from earlier prompt — kept for /_components)
// ============================================================================

type ButtonData struct {
	Label                                        string
	Variant, Size, Type, Name, Value, HxPost     string
	Loading, Disabled, FullWidth                 bool
}

type CardData struct {
	Modifier, Title, Subtitle string
	Body, Footer              template.HTML
}

type InputData struct {
	Type, Name, ID, Value, Placeholder, Autocomplete string
	Required, Disabled, ReadOnly, Invalid             bool
	Min, Max, Step                                    string
}

type FormFieldData struct {
	Label, Hint, Error string
	Input              InputData
}

type LabelData struct {
	Text, Size, Variant, For string
}

type BannerData struct {
	Variant, Title, Message, Icon string
	Action                        template.HTML
}

type BadgeData struct {
	Label, Variant string
	Dot            bool
}

type PillData struct {
	Label, Variant, Icon string
}

func componentsDemo(w http.ResponseWriter, r *http.Request) {
	funcMap := template.FuncMap{
		"mergeInputInvalid": func(in InputData, invalid bool) InputData {
			in.Invalid = invalid
			return in
		},
	}
	// Only load the partials we actually use — globbing all of them pulls
	// in audit-progress.html etc. which reference funcs (deref/toFloat)
	// that aren't in this handler's funcMap.
	files := []string{
		"templates/_components.html",
		"templates/partials/button.html",
		"templates/partials/card.html",
		"templates/partials/banner.html",
		"templates/partials/badge.html",
		"templates/partials/pill.html",
		"templates/partials/form-field.html",
		"templates/partials/input.html",
		"templates/partials/label.html",
	}
	page := template.Must(template.New("").Funcs(funcMap).ParseFiles(files...))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.ExecuteTemplate(w, "components", componentsData()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func componentsData() any {
	cardBody := template.HTML(`<p>The audit found <strong>3 critical</strong> issues across 124 products.</p>`)
	cardFooter := template.HTML(`<button type="button" class="c-button c-button--text">Dismiss</button>` +
		`<button type="button" class="c-button c-button--filled">Run audit</button>`)
	bannerAction := template.HTML(`<button type="button" class="c-button c-button--text c-button--small">Re-connect</button>`)

	type buttonGroups struct {
		Variants, Sizes, States []ButtonData
		FullWidth               ButtonData
	}

	return struct {
		Buttons    buttonGroups
		Banners    []BannerData
		Cards      []CardData
		FormFields []FormFieldData
		Inputs     []InputData
		Labels     []LabelData
		Badges     []BadgeData
		Pills      []PillData
	}{
		Buttons: buttonGroups{
			Variants: []ButtonData{
				{Label: "Filled", Variant: "filled"}, {Label: "Tonal", Variant: "tonal"},
				{Label: "Outlined", Variant: "outlined"}, {Label: "Text", Variant: "text"},
			},
			Sizes: []ButtonData{
				{Label: "Small", Variant: "filled", Size: "small"},
				{Label: "Default", Variant: "filled"},
				{Label: "Large", Variant: "filled", Size: "large"},
			},
			States: []ButtonData{
				{Label: "Loading", Variant: "filled", Loading: true},
				{Label: "Disabled", Variant: "filled", Disabled: true},
				{Label: "Disabled outlined", Variant: "outlined", Disabled: true},
			},
			FullWidth: ButtonData{Label: "Run audit on every store", Variant: "filled", FullWidth: true},
		},
		Banners: []BannerData{
			{Variant: "info", Title: "New issues detected", Message: "12 products gained warnings."},
			{Variant: "warning", Title: "Token expiring soon", Message: "Shopify token expires in 3 days.", Action: bannerAction},
			{Variant: "critical", Title: "GMC connection revoked", Message: "Re-authorize.", Action: bannerAction},
			{Variant: "success", Title: "Audit complete", Message: "All 482 products passing."},
		},
		Cards: []CardData{
			{Modifier: "elevated", Title: "Latest audit", Subtitle: "acme-electronics.myshopify.com", Body: cardBody, Footer: cardFooter},
			{Modifier: "filled", Title: "Plan usage", Subtitle: "Pro · renews May 30", Body: template.HTML(`<p>184 of 500 audits this month.</p>`)},
			{Modifier: "outlined", Title: "Team", Body: template.HTML(`<p>3 members.</p>`)},
		},
		FormFields: []FormFieldData{
			{Label: "Workspace name", Hint: "Visible to teammates.", Input: InputData{Type: "text", Name: "name", ID: "ff-name", Required: true, Placeholder: "Acme"}},
			{Label: "Email", Input: InputData{Type: "email", Name: "email", ID: "ff-email", Required: true, Autocomplete: "email"}},
			{Label: "Webhook URL", Error: "Must be valid https://.", Input: InputData{Type: "url", Name: "webhook", ID: "ff-webhook", Value: "ftp://broken", Required: true, Invalid: true}},
		},
		Inputs: []InputData{{Type: "text", Name: "t", ID: "in-t", Placeholder: "Plain text"}, {Type: "email", Name: "e", ID: "in-e"}},
		Labels: []LabelData{{Text: "Default"}, {Text: "Muted", Variant: "muted"}, {Text: "Error", Variant: "error"}},
		Badges: []BadgeData{{Label: "3"}, {Label: "OK", Variant: "success"}, {Dot: true, Variant: "primary"}},
		Pills:  []PillData{{Label: "Pro", Variant: "filled"}, {Label: "Connected", Variant: "success", Icon: "✓"}},
	}
}

