// cmd/scheduler — claims monitoring-due stores and enqueues scheduled audits.
//
// Usage:
//
//	scheduler -mode=scheduler
//
// Designed to run alongside cmd/server and cmd/worker. SIGINT/SIGTERM stops
// the loop on the next tick boundary; in-flight enqueues finish first.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/gmc"
	"github.com/example/gmcauditor/internal/mailer"
	"github.com/example/gmcauditor/internal/scheduler"
	"github.com/example/gmcauditor/internal/settings"
)

func main() {
	mode := flag.String("mode", "", "one of: scheduler")
	flag.Parse()
	switch *mode {
	case "scheduler":
		runScheduler()
	default:
		fmt.Fprintln(os.Stderr, "usage: scheduler -mode=scheduler")
		os.Exit(2)
	}
}

func runScheduler() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	dbURL := getenv("DATABASE_URL", "postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable")

	tick := 60 * time.Second
	if v := os.Getenv("SCHEDULER_TICK"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			tick = d
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	sch := &scheduler.Scheduler{
		Pool:   pool,
		Logger: logger,
		Tick:   tick,
		URLFor: storeURLFor,
	}

	// GMC refresher runs alongside the scheduled-audit loop in its own
	// goroutine. Optional — only spins up when GOOGLE_OAUTH_* + the
	// settings cipher are present.
	gmcRefresher := buildGMCRefresher(pool, logger)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		logger.Info("scheduler_shutting_down", slog.Duration("drain_deadline", 30*time.Second))
		cancel()
	}()

	// GMC refresher (if wired) shares the same cancel-on-signal context.
	// We wait for it to return before exiting so the last enqueue/refresh
	// either completes or is logged as timed out.
	refresherDone := make(chan struct{})
	if gmcRefresher != nil {
		go func() {
			gmcRefresher.Run(ctx)
			close(refresherDone)
		}()
	} else {
		close(refresherDone)
	}

	if err := sch.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("scheduler_exit", slog.Any("err", err))
		os.Exit(1)
	}
	// Bound the wait on the refresher to 30s.
	select {
	case <-refresherDone:
		logger.Info("scheduler_shutdown_clean")
	case <-time.After(30 * time.Second):
		logger.Warn("scheduler_drain_timeout", slog.String("hint", "GMC refresher exceeded 30s"))
	}
}

// buildGMCRefresher wires the optional Google background-sync loop. Mirrors
// the OAuth wiring in cmd/server / cmd/worker — keeps cred plumbing local
// to each binary so the scheduler stays standalone.
func buildGMCRefresher(pool *pgxpool.Pool, logger *slog.Logger) *scheduler.GMCRefresher {
	id := os.Getenv("GOOGLE_OAUTH_CLIENT_ID")
	secret := os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET")
	if id == "" || secret == "" {
		logger.Info("gmc_refresher_disabled", slog.String("reason", "GOOGLE_OAUTH_CLIENT_ID/SECRET unset"))
		return nil
	}
	cipher, err := settings.NewCipherFromEnv()
	if err != nil {
		logger.Warn("gmc_refresher_disabled", slog.String("reason", "SETTINGS_ENCRYPTION_KEY unset"))
		return nil
	}
	oauth := &gmc.OAuth{
		ClientID:     id,
		ClientSecret: secret,
		RedirectURL:  getenv("GOOGLE_OAUTH_REDIRECT_URL", "http://localhost:8080/oauth/google/callback"),
		AuthURL:      os.Getenv("GOOGLE_OAUTH_AUTH_URL"),
		TokenURL:     os.Getenv("GOOGLE_OAUTH_TOKEN_URL"),
		RevokeURL:    os.Getenv("GOOGLE_OAUTH_REVOKE_URL"),
	}
	conns := gmc.NewConnectionStore(pool, cipher, oauth)
	smtpHost := getenv("SMTP_HOST", "localhost")
	smtpPort := getenv("SMTP_PORT", "1025")
	mailFrom := getenv("SMTP_FROM", "noreply@gmcauditor.local")
	baseURL := getenv("APP_BASE_URL", "http://localhost:8080")
	mail := mailer.NewSMTPMailer(mailer.SMTPConfig{Host: smtpHost, Port: smtpPort, From: mailFrom}, logger)

	return &scheduler.GMCRefresher{
		Pool:       pool,
		Conns:      conns,
		GMCBaseURL: os.Getenv("GMC_BASE_URL"),
		Logger:     logger,
		Tick:       parseTick(os.Getenv("GMC_REFRESH_TICK"), time.Minute),
		OnUnauthorized: func(ctx context.Context, conn *gmc.Connection) {
			gmc.NotifyOwnerOfRevoke(ctx, pool, mail, baseURL, mailFrom, logger, conn)
		},
	}
}

func parseTick(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

// storeURLFor mirrors web.StoreURLFor so scheduler-built jobs crawl the
// same URLs as manual audits. localhost / 127.* always uses http://;
// everything else uses https://. Both code paths must agree — a copy is
// the cheapest way to keep the scheduler binary's deps small.
func storeURLFor(shopDomain string) string {
	d := strings.TrimSpace(shopDomain)
	if d == "" {
		return ""
	}
	host := d
	if u, err := url.Parse(d); err == nil && u.Host != "" {
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	scheme := "https"
	if host == "localhost" || strings.HasPrefix(host, "127.") {
		scheme = "http"
	}
	if !strings.HasPrefix(d, "http://") && !strings.HasPrefix(d, "https://") {
		return scheme + "://" + d
	}
	return d
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
