// cmd/worker — drains audit_jobs from Postgres and runs the audit pipeline
// for each. Designed to run alongside cmd/server in the same docker image.
//
// Usage:
//
//	worker -mode=worker
//
// The -mode flag is required so future modes (scheduler, sweeper) can share
// the same binary. The worker process exits gracefully on SIGINT / SIGTERM,
// draining in-flight jobs first.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/ai"
	"github.com/example/gmcauditor/internal/audit"
	_ "github.com/example/gmcauditor/internal/audit/checks"
	"github.com/example/gmcauditor/internal/audit/differ"
	"github.com/example/gmcauditor/internal/crawler"
	"github.com/example/gmcauditor/internal/gmc"
	"github.com/example/gmcauditor/internal/jobs"
	"github.com/example/gmcauditor/internal/mailer"
	"github.com/example/gmcauditor/internal/monitoring"
	"github.com/example/gmcauditor/internal/settings"
	"github.com/example/gmcauditor/internal/store"
)

func main() {
	mode := flag.String("mode", "", "one of: worker")
	flag.Parse()
	switch *mode {
	case "worker":
		runWorker()
	default:
		fmt.Fprintln(os.Stderr, "usage: worker -mode=worker")
		os.Exit(2)
	}
}

func runWorker() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	dbURL := getenv("DATABASE_URL", "postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	st := store.NewStore(pool)
	persister := &audit.PgPersister{Pool: pool, Logger: logger}

	// Dispatcher fires alert emails after each successful audit. The worker
	// runs without RLS context (gmc DB role has BYPASSRLS), so it can read
	// store_alert_subscriptions across tenants.
	smtpHost := getenv("SMTP_HOST", "localhost")
	smtpPort := getenv("SMTP_PORT", "1025")
	mailFrom := getenv("SMTP_FROM", "noreply@gmcauditor.local")
	baseURL := getenv("APP_BASE_URL", "http://localhost:8080")
	appSecret := getenv("APP_SECRET", "gmcauditor-dev-secret-not-for-prod")
	mail := mailer.NewSMTPMailer(mailer.SMTPConfig{Host: smtpHost, Port: smtpPort, From: mailFrom}, logger)
	dispatcher := &monitoring.Dispatcher{
		Pool:      pool,
		Mailer:    mail,
		BaseURL:   baseURL,
		MailFrom:  mailFrom,
		AppSecret: []byte(appSecret),
		Logger:    logger,
	}

	settingsCipher, settingsErr := settings.NewCipherFromEnv()
	if settingsErr != nil {
		ephemeral := make([]byte, 32)
		_, _ = rand.Read(ephemeral)
		settingsCipher, _ = settings.NewCipher(base64.StdEncoding.EncodeToString(ephemeral))
		logger.Warn("settings_ephemeral_key", slog.String("reason", "SETTINGS_ENCRYPTION_KEY not set"))
	}
	settingsSvc := settings.New(settingsCipher,
		settings.NewPostgresBackend(pool),
		settings.NewPostgresAuditor(pool),
	)

	// Real AI client by default; fall back to the deterministic mock when
	// no API key is configured (typical in dev).
	var aiClient ai.Client = ai.NewOpenAIClient(settingsAdapter{settingsSvc}, ai.WithLogger(logger))
	if v, _ := settingsSvc.Get(ctx, settings.KeyAIAPIKey); v == "" {
		logger.Info("ai_using_mock", slog.String("reason", "no AI key configured"))
		aiClient = ai.NewMockClient()
	}

	// GMC OAuth + connection store (only wired if env vars set). Same
	// pattern as cmd/server: handlers tolerate nil, the pipeline's
	// GMCSyncFn closure short-circuits when nil.
	var gmcOAuth *gmc.OAuth
	var gmcConns *gmc.ConnectionStore
	if id := os.Getenv("GOOGLE_OAUTH_CLIENT_ID"); id != "" && os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET") != "" {
		gmcOAuth = &gmc.OAuth{
			ClientID:     id,
			ClientSecret: os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"),
			RedirectURL:  getenv("GOOGLE_OAUTH_REDIRECT_URL", "http://localhost:8080/oauth/google/callback"),
			AuthURL:      os.Getenv("GOOGLE_OAUTH_AUTH_URL"),
			TokenURL:     os.Getenv("GOOGLE_OAUTH_TOKEN_URL"),
			RevokeURL:    os.Getenv("GOOGLE_OAUTH_REVOKE_URL"),
		}
		gmcConns = gmc.NewConnectionStore(pool, settingsCipher, gmcOAuth)
		logger.Info("gmc_oauth_configured")
	}
	gmcBaseURL := os.Getenv("GMC_BASE_URL")

	persister.AfterCommit = func(ctx context.Context, in audit.AuditInput, out *audit.AuditOutput, d *differ.Diff) {
		// Run dispatch in a fresh context — the parent ctx may already be
		// cancelled by the time the worker is draining and we still want
		// the email to go out.
		go dispatcher.Dispatch(context.Background(), monitoring.AuditResult{
			AuditID:   in.AuditID,
			TenantID:  in.TenantID,
			StoreID:   in.StoreID,
			StoreName: in.StoreName,
			Status: func() string {
				for _, s := range out.Stages {
					if s.Status == "failed" {
						return "failed"
					}
				}
				return "succeeded"
			}(),
			Score:     out.Score,
			RiskLevel: out.RiskLevel,
		}, d)
	}

	gmcSync := buildGMCSync(pool, gmcConns, gmcBaseURL, mail, baseURL, mailFrom, logger)

	pipeline := &audit.Pipeline{
		Crawl: func(ctx context.Context, storeURL string) (audit.CheckContext, error) {
			c, err := crawler.New(storeURL)
			if err != nil {
				return audit.CheckContext{}, err
			}
			return c.Crawl(ctx)
		},
		GMC:      gmcSync,
		AI:       aiClient,
		Persist:  persister,
		Progress: persister,
		Logger:   logger,
	}

	worker := jobs.NewWorker(pool, logger)
	worker.Register(jobs.KindAuditStore, &auditHandler{
		Pool: pool, Store: st, Pipeline: pipeline, Logger: logger,
	})

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		logger.Info("signal_received_draining")
		cancel()
	}()

	if err := worker.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("worker_exit", slog.Any("err", err))
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// audit handler
// ----------------------------------------------------------------------------

type auditPayload struct {
	AuditID      uuid.UUID  `json:"audit_id"`
	TenantID     uuid.UUID  `json:"tenant_id"`
	StoreID      uuid.UUID  `json:"store_id"`
	StoreURL     string     `json:"store_url"`
	StoreName    string     `json:"store_name"`
	StoreContext string     `json:"store_context,omitempty"`
	Trigger      string     `json:"trigger"`
	TriggeredBy  *uuid.UUID `json:"triggered_by,omitempty"`
}

type auditHandler struct {
	Pool     *pgxpool.Pool
	Store    *store.Store
	Pipeline *audit.Pipeline
	Logger   *slog.Logger
}

func (h *auditHandler) Handle(ctx context.Context, j jobs.Job) error {
	var p auditPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	out, err := h.Pipeline.Run(ctx, audit.AuditInput{
		AuditID:      p.AuditID,
		TenantID:     p.TenantID,
		StoreID:      p.StoreID,
		StoreURL:     p.StoreURL,
		StoreName:    p.StoreName,
		StoreContext: p.StoreContext,
		TriggeredBy:  p.TriggeredBy,
		Trigger:      p.Trigger,
	})
	if err != nil {
		return err
	}
	h.Logger.Info("audit_done",
		slog.String("audit_id", p.AuditID.String()),
		slog.Int("score", out.Score),
		slog.String("risk", out.RiskLevel),
		slog.Int("issues", out.Counts["critical"]+out.Counts["error"]+out.Counts["warning"]),
	)
	return nil
}

// settingsAdapter bridges *settings.Service to ai.SettingsProvider.
type settingsAdapter struct{ s *settings.Service }

func (a settingsAdapter) Get(ctx context.Context, key string) (string, error) {
	return a.s.Get(ctx, key)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
