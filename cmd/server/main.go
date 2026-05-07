package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/auth"
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
		{Name: "admin-login", Layout: "templates/layouts/public.html", Template: "templates/pages/admin-login.html"},
		{Name: "admin-totp-enroll", Layout: "templates/layouts/public.html", Template: "templates/pages/admin-totp-enroll.html"},
		{Name: "admin-totp-verify", Layout: "templates/layouts/public.html", Template: "templates/pages/admin-totp-verify.html"},
		{Name: "admin-dashboard", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-dashboard.html"},
		{Name: "admin-tenants", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-tenants.html"},
		{Name: "admin-tenant-detail", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-tenant-detail.html"},
		{Name: "admin-audit-log", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-audit-log.html"},
		{Name: "admin-settings", Layout: "templates/layouts/platform.html", Template: "templates/pages/admin-settings.html"},
	}
	if err := rend.Register(pages); err != nil {
		log.Fatalf("register pages: %v", err)
	}

	csrfMgr := auth.NewCSRFManager(hashKey)
	h := &web.Handlers{
		Pool: pool, Store: st, Renderer: rend,
		Cookies: cookies, Sessions: sessions, CSRF: csrfMgr,
		Mailer: mail, BaseURL: baseURL, MailFrom: mailFrom,
		LoginLimit: web.NewLoginLimiter(),
		Logger:     logger,
	}

	mux := http.NewServeMux()

	// Static + components demo (carry-over from prompt 7).
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("GET /_components", componentsDemo)

	// Public routes.
	mux.HandleFunc("GET /{$}", h.Landing)
	mux.HandleFunc("GET /pricing", h.Pricing)
	mux.HandleFunc("GET /features", h.Features)
	mux.HandleFunc("GET /signup", h.SignupForm)
	mux.HandleFunc("POST /signup", h.Signup)
	mux.HandleFunc("GET /login", h.LoginForm)
	mux.HandleFunc("POST /login", h.Login)
	mux.HandleFunc("POST /logout", h.Logout)
	mux.HandleFunc("GET /verify-email/{token}", h.VerifyEmail)
	mux.HandleFunc("POST /verify-email/resend", h.ResendVerification)
	mux.HandleFunc("GET /forgot-password", h.ForgotForm)
	mux.HandleFunc("POST /forgot-password", h.Forgot)
	mux.HandleFunc("GET /reset-password/{token}", h.ResetForm)
	mux.HandleFunc("POST /reset-password/{token}", h.Reset)
	mux.HandleFunc("GET /invitations/{token}", h.Invitation)
	mux.HandleFunc("POST /invitations/{token}/accept", h.AcceptInvitation)

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
	mux.Handle("GET /t/{slug}/members", tenantMW(http.HandlerFunc(h.MembersPage)))
	mux.Handle("POST /t/{slug}/invitations", ownerMW(http.HandlerFunc(h.CreateInvitation)))
	mux.Handle("POST /t/{slug}/invitations/{id}/revoke", ownerMW(http.HandlerFunc(h.RevokeInvitation)))
	mux.Handle("POST /t/{slug}/memberships/{user_id}/remove", ownerMW(http.HandlerFunc(h.RemoveMembership)))
	mux.Handle("POST /t/{slug}/transfer-ownership", ownerMW(http.HandlerFunc(h.TransferOwnership)))
	mux.Handle("POST /t/{slug}/delete", ownerMW(http.HandlerFunc(h.DeleteTenant)))

	// Platform admin routes.
	settingsBackend := settings.NewPostgresBackend(pool)
	settingsAuditor := settings.NewPostgresAuditor(pool)
	settingsCipher, settingsErr := settings.NewCipherFromEnv()
	if settingsErr != nil {
		// In dev we tolerate a missing key by generating an ephemeral one.
		// Production boot guards this in cmd/server (TODO).
		ephemeral := make([]byte, 32)
		_, _ = rand.Read(ephemeral)
		settingsCipher, _ = settings.NewCipher(base64.StdEncoding.EncodeToString(ephemeral))
		logger.Warn("settings: SETTINGS_ENCRYPTION_KEY not set, using ephemeral key for this run")
	}
	settingsSvc := settings.New(settingsCipher, settingsBackend, settingsAuditor)
	adminH := &web.AdminHandlers{
		Pool: pool, Store: st, Renderer: rend,
		Cookies: cookies, Sessions: sessions, CSRF: csrfMgr,
		Mailer: mail, Settings: settingsSvc, BaseURL: baseURL, MailFrom: mailFrom,
		Logger: logger,
	}

	mux.HandleFunc("GET /admin/login", adminH.LoginForm)
	mux.HandleFunc("POST /admin/login", adminH.Login)
	mux.HandleFunc("GET /admin/totp/enroll", adminH.TOTPEnrollForm)
	mux.HandleFunc("POST /admin/totp/enroll", adminH.TOTPEnroll)
	mux.HandleFunc("GET /admin/totp/verify", adminH.TOTPVerifyForm)
	mux.HandleFunc("POST /admin/totp/verify", adminH.TOTPVerify)
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
	logger.Info("shutting down")
	shutdownCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShut()
	_ = srv.Shutdown(shutdownCtx)
}

func cookieKeys(appSecret string) (hashKey, blockKey []byte) {
	h := sha512.Sum512([]byte("hash:" + appSecret))
	b := sha256.Sum256([]byte("block:" + appSecret))
	return h[:32], b[:]
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
	partials, _ := filepath.Glob("templates/partials/*.html")
	files := append([]string{"templates/_components.html"}, partials...)
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

