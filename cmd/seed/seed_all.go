package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/auth"
)

// seedAll repopulates the development database with a small, realistic
// dataset: 1 super_admin, 2 tenants, 4 users with mixed memberships, 6
// stores, 4 completed audits + 1 failed + 1 queued, 2 pending invitations,
// 3 purchases, 1 GMC connection, monitoring enabled on 2 stores.
//
// Idempotent: every row uses a deterministic UUID derived from a
// namespace + label, so re-running upserts cleanly. Audit issue rows are
// scoped per-audit and replaced via DELETE+INSERT in a tx.
//
// Reads SEED_ADMIN_EMAIL + SEED_ADMIN_PASSWORD for the platform admin.
func seedAll(ctx context.Context, pool *pgxpool.Pool) error {
	adminEmail := getenv("SEED_ADMIN_EMAIL", "admin@gmcauditor.local")
	adminPw := getenv("SEED_ADMIN_PASSWORD", "super-strong-pass-2026")
	commonPw := "super-strong-pass-2026" // shared dev password for the 4 users

	// Deterministic UUID namespace so re-runs hit the same rows.
	ns := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id := func(label string) uuid.UUID { return uuid.NewSHA1(ns, []byte(label)) }

	// ---- Users ----
	commonHash, err := auth.HashPassword(commonPw)
	if err != nil {
		return fmt.Errorf("hash common password: %w", err)
	}
	adminHash, err := auth.HashPassword(adminPw)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}

	type seedUser struct {
		ID           uuid.UUID
		Email        string
		Name         string
		Hash         string
	}
	users := []seedUser{
		{id("user:admin"), adminEmail, "Platform Admin", adminHash},
		{id("user:sarah"), "sarah@sarahsshop.example", "Sarah Mitchell", commonHash},
		{id("user:alex"), "alex@growthcollective.example", "Alex Okafor", commonHash},
		{id("user:ben"), "ben@growthcollective.example", "Ben Tanaka", commonHash},
		{id("user:claire"), "claire@growthcollective.example", "Claire Levesque", commonHash},
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, u := range users {
		_, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, name, password_hash, email_verified_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, now(), now(), now())
			ON CONFLICT (id) DO UPDATE SET
			  email = EXCLUDED.email,
			  name  = EXCLUDED.name,
			  password_hash = EXCLUDED.password_hash,
			  email_verified_at = EXCLUDED.email_verified_at,
			  updated_at = now()
		`, u.ID, u.Email, u.Name, u.Hash)
		if err != nil {
			return fmt.Errorf("upsert user %s: %w", u.Email, err)
		}
	}

	// ---- Platform admin ----
	if _, err := tx.Exec(ctx, `
		INSERT INTO platform_admins (id, user_id, role, created_at)
		VALUES ($1, $2, 'super', now())
		ON CONFLICT (user_id) DO UPDATE SET role = 'super'
	`, id("admin:row"), id("user:admin")); err != nil {
		return fmt.Errorf("upsert admin: %w", err)
	}

	// ---- Tenants ----
	type seedTenant struct {
		ID    uuid.UUID
		Name  string
		Slug  string
		Kind  string
		Plan  string
	}
	tenants := []seedTenant{
		{id("tenant:sarahs-shop"),    "Sarah's Shop",      "sarahs-shop",      "individual", "starter"},
		{id("tenant:growth-collective"), "Growth Collective", "growth-collective", "agency",  "agency"},
	}
	for _, t := range tenants {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenants (id, name, slug, kind, plan, plan_renews_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4::tenant_kind, $5::plan_tier, now() + interval '1 month', now(), now())
			ON CONFLICT (id) DO UPDATE SET
			  name = EXCLUDED.name, kind = EXCLUDED.kind,
			  plan = EXCLUDED.plan, plan_renews_at = EXCLUDED.plan_renews_at,
			  updated_at = now()
		`, t.ID, t.Name, t.Slug, t.Kind, t.Plan); err != nil {
			return fmt.Errorf("upsert tenant %s: %w", t.Slug, err)
		}
	}

	// Default tenant per user (lets login redirect to the right workspace).
	if _, err := tx.Exec(ctx, `UPDATE users SET default_tenant_id = $2 WHERE id = $1`,
		id("user:sarah"), id("tenant:sarahs-shop")); err != nil {
		return err
	}
	for _, label := range []string{"user:alex", "user:ben", "user:claire"} {
		if _, err := tx.Exec(ctx, `UPDATE users SET default_tenant_id = $2 WHERE id = $1`,
			id(label), id("tenant:growth-collective")); err != nil {
			return err
		}
	}

	// ---- Memberships ----
	type seedMembership struct {
		ID       uuid.UUID
		TenantID uuid.UUID
		UserID   uuid.UUID
		Role     string
	}
	memberships := []seedMembership{
		{id("mem:sarah-sarahs"),     id("tenant:sarahs-shop"),       id("user:sarah"),  "owner"},
		{id("mem:alex-growth"),      id("tenant:growth-collective"), id("user:alex"),   "owner"},
		{id("mem:ben-growth"),       id("tenant:growth-collective"), id("user:ben"),    "member"},
		{id("mem:claire-growth"),    id("tenant:growth-collective"), id("user:claire"), "member"},
		// Cross-membership: Claire is also a member of Sarah's Shop.
		{id("mem:claire-sarahs"),    id("tenant:sarahs-shop"),       id("user:claire"), "member"},
	}
	for _, m := range memberships {
		if _, err := tx.Exec(ctx, `
			INSERT INTO memberships (id, tenant_id, user_id, role, created_at, updated_at)
			VALUES ($1, $2, $3, $4::membership_role, now(), now())
			ON CONFLICT (id) DO UPDATE SET role = EXCLUDED.role, updated_at = now()
		`, m.ID, m.TenantID, m.UserID, m.Role); err != nil {
			return fmt.Errorf("upsert membership %s: %w", m.ID, err)
		}
	}

	// ---- Stores ----
	type seedStore struct {
		ID           uuid.UUID
		TenantID     uuid.UUID
		Domain       string
		DisplayName  string
		MonitorEnabled bool
		MonitorFreq    string // interval literal
	}
	stores := []seedStore{
		{id("store:sarahs"), id("tenant:sarahs-shop"), "sarahs-soap-co.myshopify.com", "Sarah's Soap Co.", true, "7 days"},
		{id("store:aurelia"), id("tenant:growth-collective"), "aurelia-skincare.myshopify.com", "Aurelia Skincare", true, "1 day"},
		{id("store:finch"), id("tenant:growth-collective"), "finch-coffee.myshopify.com", "Finch Coffee Roasters", false, "7 days"},
		{id("store:kettle"), id("tenant:growth-collective"), "kettle-and-knit.myshopify.com", "Kettle & Knit", false, "7 days"},
		{id("store:northbay"), id("tenant:growth-collective"), "north-bay-bicycles.myshopify.com", "North Bay Bicycles", false, "7 days"},
		{id("store:plover"), id("tenant:growth-collective"), "plover-eyewear.myshopify.com", "Plover Eyewear", false, "7 days"},
	}
	for _, s := range stores {
		if _, err := tx.Exec(ctx, `
			INSERT INTO stores (id, tenant_id, shop_domain, display_name, status,
			                    monitor_enabled, monitor_frequency, monitor_alert_threshold,
			                    next_audit_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'connected'::store_status,
			        $5, $6::interval, 'warning'::issue_severity,
			        CASE WHEN $5 THEN now() + interval '1 day' ELSE NULL END,
			        now(), now())
			ON CONFLICT (id) DO UPDATE SET
			  display_name = EXCLUDED.display_name,
			  monitor_enabled = EXCLUDED.monitor_enabled,
			  monitor_frequency = EXCLUDED.monitor_frequency,
			  next_audit_at = EXCLUDED.next_audit_at,
			  updated_at = now()
		`, s.ID, s.TenantID, s.Domain, s.DisplayName, s.MonitorEnabled, s.MonitorFreq); err != nil {
			return fmt.Errorf("upsert store %s: %w", s.Domain, err)
		}
	}

	// ---- Audits ----
	if err := seedAudits(ctx, tx, id); err != nil {
		return err
	}

	// ---- Invitations (2 pending) ----
	if err := seedInvitations(ctx, tx, id); err != nil {
		return err
	}

	// ---- Purchases (3) ----
	if err := seedPurchases(ctx, tx, id); err != nil {
		return err
	}

	// ---- GMC connection on Aurelia ----
	if err := seedGMC(ctx, tx, id); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed tx: %w", err)
	}

	log.Printf("seed-all complete: 1 admin (%s), %d tenants, %d users, %d stores",
		adminEmail, len(tenants), len(users), len(stores))
	return nil
}

// seedAudits inserts 4 succeeded audits + 1 failed + 1 queued, plus per-audit
// issues with realistic check IDs. Scores: 32, 67, 78, 91.
func seedAudits(ctx context.Context, tx pgx.Tx, id func(string) uuid.UUID) error {
	type seedAudit struct {
		Label    string
		StoreID  uuid.UUID
		TenantID uuid.UUID
		Status   string
		Score    *int
		Risk     *string
		Trigger  string
		Issues   []seedIssue
		Summary  string
		FinishedAgo time.Duration
		Error    string
	}
	scoreP := func(n int) *int { s := n; return &s }
	riskP := func(s string) *string { return &s }
	mk := func(s string) []seedIssue { return nil }
	_ = mk

	audits := []seedAudit{
		// Sarah's Shop — score 32 (critical condition)
		{
			Label:   "audit:sarahs-1",
			StoreID: id("store:sarahs"),
			TenantID: id("tenant:sarahs-shop"),
			Status:  "succeeded",
			Score:   scoreP(32), Risk: riskP("high"),
			Trigger: "manual",
			FinishedAgo: 6 * 24 * time.Hour,
			Summary: "Three critical issues are blocking GMC approval. Address HTTPS and shipping policy before submitting any feeds.",
			Issues: []seedIssue{
				{Rule: "https_everywhere", Sev: "critical", Title: "Store and outbound resources use HTTPS", Detail: "12 product images load over http://; mixed-content warnings break checkout for Safari users."},
				{Rule: "shipping_policy_linked", Sev: "critical", Title: "Shipping policy linked from the homepage", Detail: "No /policies/shipping link found in the footer."},
				{Rule: "product_schema_complete", Sev: "critical", Title: "Product pages emit JSON-LD structured data", Detail: "5 of 14 products are missing the JSON-LD <script> block."},
				{Rule: "product_description_quality", Sev: "warning", Title: "Product descriptions are substantial, unique, and free of raw HTML", Detail: "8 products have fewer than 150 characters of description."},
				{Rule: "title_format", Sev: "warning", Title: "Product titles are descriptive without ALL-CAPS", Detail: "3 titles are written entirely in capitals."},
			},
		},
		// Aurelia — score 67 (medium)
		{
			Label: "audit:aurelia-1",
			StoreID: id("store:aurelia"),
			TenantID: id("tenant:growth-collective"),
			Status: "succeeded",
			Score: scoreP(67), Risk: riskP("medium"),
			Trigger: "scheduled",
			FinishedAgo: 12 * time.Hour,
			Summary: "One critical and a handful of warnings — high-impact fixes are concentrated in the Returns flow.",
			Issues: []seedIssue{
				{Rule: "refund_policy_quality", Sev: "critical", Title: "Refund/return policy clearly states timeframe and process", Detail: "Refund policy is two sentences and doesn't state a return window."},
				{Rule: "image_alt_text", Sev: "warning", Title: "Product images carry meaningful alt text", Detail: "23 product images have empty alt attributes."},
				{Rule: "long_description", Sev: "warning", Title: "Product descriptions exceed Google's 150-char floor", Detail: "4 products are below the threshold."},
			},
		},
		// Finch — score 78 (medium-good)
		{
			Label: "audit:finch-1",
			StoreID: id("store:finch"),
			TenantID: id("tenant:growth-collective"),
			Status: "succeeded",
			Score: scoreP(78), Risk: riskP("medium"),
			Trigger: "manual",
			FinishedAgo: 48 * time.Hour,
			Summary: "Minor polish needed — schema markup is solid; tighten contact info on the homepage.",
			Issues: []seedIssue{
				{Rule: "contact_info_visible", Sev: "warning", Title: "Contact info reachable from the homepage", Detail: "Phone number is visible only on the Contact page; consider promoting to the footer."},
				{Rule: "broken_product_links", Sev: "warning", Title: "Sitemap product URLs all return 200", Detail: "1 sitemap URL returned 410 Gone."},
			},
		},
		// Kettle — score 91 (good)
		{
			Label: "audit:kettle-1",
			StoreID: id("store:kettle"),
			TenantID: id("tenant:growth-collective"),
			Status: "succeeded",
			Score: scoreP(91), Risk: riskP("low"),
			Trigger: "manual",
			FinishedAgo: 36 * time.Hour,
			Summary: "Solid foundation. One non-blocking warning about the About page depth.",
			Issues: []seedIssue{
				{Rule: "about_page_exists", Sev: "warning", Title: "About page exists with real, substantive content", Detail: "About page is 84 words; consider expanding to ≥200 for clearer brand signal."},
			},
		},
		// North Bay — failed audit
		{
			Label: "audit:northbay-failed",
			StoreID: id("store:northbay"),
			TenantID: id("tenant:growth-collective"),
			Status: "failed",
			Trigger: "scheduled",
			FinishedAgo: 4 * time.Hour,
			Error: "crawl: connect timeout after 30s — store responded HTTP 503 on every retry",
			Summary: "",
		},
		// Plover — queued (in progress, not yet picked up)
		{
			Label: "audit:plover-queued",
			StoreID: id("store:plover"),
			TenantID: id("tenant:growth-collective"),
			Status: "queued",
			Trigger: "manual",
		},
	}

	for _, a := range audits {
		auditID := id(a.Label)
		var startedAt, finishedAt interface{}
		if a.Status != "queued" {
			t := time.Now().Add(-a.FinishedAgo - 30*time.Second)
			startedAt = t
			finishedAt = time.Now().Add(-a.FinishedAgo)
		}
		counts := map[string]int{"critical": 0, "error": 0, "warning": 0, "info": 0}
		for _, i := range a.Issues {
			counts[i.Sev]++
		}
		countsJSON, _ := json.Marshal(counts)

		var triggeredBy interface{}
		if a.Trigger == "manual" {
			// Sarah for sarahs, Alex otherwise
			if a.TenantID == id("tenant:sarahs-shop") {
				triggeredBy = id("user:sarah")
			} else {
				triggeredBy = id("user:alex")
			}
		}
		var summaryArg, riskArg interface{}
		if a.Summary != "" {
			summaryArg = a.Summary
		}
		if a.Risk != nil {
			riskArg = *a.Risk
		}
		var errorArg interface{}
		if a.Error != "" {
			errorArg = a.Error
		}

		nextStepsJSON := []byte("null")
		if a.Status == "succeeded" {
			steps := []string{
				"Open this store's report and start with the critical-tier issues.",
				"Re-run the audit after each fix to confirm the rule cleared.",
				"Enable monitoring so we re-check before the next ad campaign.",
			}
			nextStepsJSON, _ = json.Marshal(steps)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO audits (id, tenant_id, store_id, triggered_by, trigger, status,
			                   started_at, finished_at, score, risk_level, summary,
			                   next_steps, issue_counts, progress, error_message,
			                   created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6::audit_status,
			        $7, $8, $9, $10, $11,
			        $12, $13, '[]'::jsonb, $14,
			        COALESCE($8, now()), now())
			ON CONFLICT (id) DO UPDATE SET
			  status = EXCLUDED.status, score = EXCLUDED.score,
			  risk_level = EXCLUDED.risk_level, summary = EXCLUDED.summary,
			  next_steps = EXCLUDED.next_steps, issue_counts = EXCLUDED.issue_counts,
			  finished_at = EXCLUDED.finished_at, error_message = EXCLUDED.error_message,
			  updated_at = now()
		`, auditID, a.TenantID, a.StoreID, triggeredBy, a.Trigger, a.Status,
			startedAt, finishedAt, deref(a.Score), riskArg, summaryArg,
			nextStepsJSON, countsJSON, errorArg); err != nil {
			return fmt.Errorf("upsert audit %s: %w", a.Label, err)
		}

		// Replace issues for this audit.
		if _, err := tx.Exec(ctx, `DELETE FROM issues WHERE audit_id = $1`, auditID); err != nil {
			return err
		}
		for i, iss := range a.Issues {
			if _, err := tx.Exec(ctx, `
				INSERT INTO issues (id, tenant_id, audit_id, store_id,
				                   rule_code, severity, status, title, description,
				                   source, created_at, updated_at)
				VALUES ($1, $2, $3, $4,
				        $5, $6::issue_severity, 'open'::issue_status, $7, $8,
				        'crawler', now(), now())
			`, id(fmt.Sprintf("%s:issue:%d", a.Label, i)), a.TenantID, auditID, a.StoreID,
				iss.Rule, iss.Sev, iss.Title, iss.Detail); err != nil {
				return fmt.Errorf("insert issue %s/%d: %w", a.Label, i, err)
			}
		}
	}
	return nil
}

type seedIssue struct {
	Rule, Sev, Title, Detail string
}

// deref unwraps *int to int (used for INSERT placeholders that allow NULL).
func deref(p *int) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

// seedInvitations inserts 2 pending invitations (one per tenant).
func seedInvitations(ctx context.Context, tx pgx.Tx, id func(string) uuid.UUID) error {
	// invitations.token_hash is sha256(random); for the seed we use deterministic
	// 32-byte values so re-running stays idempotent and the URL can be
	// reconstructed from the row if needed.
	rows := []struct {
		ID, TenantID uuid.UUID
		InviterID    uuid.UUID
		Email        string
		Role         string
	}{
		{id("inv:1"), id("tenant:sarahs-shop"),       id("user:sarah"), "kim@bayroasters.example", "member"},
		{id("inv:2"), id("tenant:growth-collective"), id("user:alex"),  "marco@growthcollective.example", "member"},
	}
	for _, r := range rows {
		// 32 bytes derived from the inv id so it's stable.
		hash := sha256Bytes("seed-invitation:" + r.ID.String())
		if _, err := tx.Exec(ctx, `
			INSERT INTO invitations (id, tenant_id, inviter_id, email, role, token_hash,
			                         expires_at, accepted_at, created_at)
			VALUES ($1, $2, $3, $4, $5::membership_role, $6,
			        now() + interval '7 days', NULL, now())
			ON CONFLICT (id) DO NOTHING
		`, r.ID, r.TenantID, r.InviterID, r.Email, r.Role, hash); err != nil {
			return fmt.Errorf("insert invitation %s: %w", r.ID, err)
		}
	}
	return nil
}

// seedPurchases inserts 3 historical purchases tied to the seeded tenants.
func seedPurchases(ctx context.Context, tx pgx.Tx, id func(string) uuid.UUID) error {
	rows := []struct {
		ID         uuid.UUID
		TenantID   uuid.UUID
		SaleID     string
		ProductID  string
		Plan       string
		Amount     int
		IsRecurring bool
		PurchasedAgo time.Duration
	}{
		{id("purchase:sarahs-starter"),     id("tenant:sarahs-shop"),       "seed-starter-sarahs",   "gmc-starter", "starter", 1200,  true, 14 * 24 * time.Hour},
		{id("purchase:growth-agency"),      id("tenant:growth-collective"), "seed-agency-growth",    "gmc-agency",  "agency",  19900, true, 21 * 24 * time.Hour},
		{id("purchase:sarahs-rescue"),      id("tenant:sarahs-shop"),       "seed-rescue-sarahs",    "gmc-rescue",  "free",    2900,  false, 3 * 24 * time.Hour},
	}
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchases (id, tenant_id, gumroad_sale_id, product_id,
			                      plan, amount_cents, currency, status,
			                      purchased_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4,
			        $5::plan_tier, $6, 'USD', 'active'::purchase_status,
			        $7, $7, now())
			ON CONFLICT (id) DO UPDATE SET
			  amount_cents = EXCLUDED.amount_cents,
			  status       = EXCLUDED.status,
			  updated_at   = now()
		`, r.ID, r.TenantID, r.SaleID, r.ProductID, r.Plan, r.Amount,
			time.Now().Add(-r.PurchasedAgo)); err != nil {
			return fmt.Errorf("insert purchase %s: %w", r.SaleID, err)
		}
	}
	return nil
}

// seedGMC seeds an active GMC connection on Aurelia (Growth Collective).
// Uses a fake encrypted blob — the dispatcher won't try to talk to Google
// without a working refresh token, but the row exposes the connection card
// + the dashboard "X of Y connected" count.
func seedGMC(ctx context.Context, tx pgx.Tx, id func(string) uuid.UUID) error {
	// Insert a placeholder blob; SETTINGS_ENCRYPTION_KEY may not be set in
	// every dev env, so we use \x00 bytes that will fail decrypt-time but
	// keep the row visible.
	dummyEnc := []byte{0xde, 0xad, 0xbe, 0xef}
	if _, err := tx.Exec(ctx, `
		INSERT INTO store_gmc_connections (id, tenant_id, store_id, merchant_id,
		                                   account_email,
		                                   refresh_token_encrypted, token_nonce, token_expires_at,
		                                   status, scope,
		                                   account_status, warnings_count, suspensions_count,
		                                   website_claimed,
		                                   last_sync_at, last_sync_status,
		                                   created_at, updated_at)
		VALUES ($1, $2, $3, '111222333',
		        'alex+gmc@growthcollective.example',
		        $4::bytea, ''::bytea, now() + interval '1 hour',
		        'active'::gmc_connection_status, 'https://www.googleapis.com/auth/content',
		        'warning', 1, 0,
		        true,
		        now() - interval '15 minutes', 'ok',
		        now(), now())
		ON CONFLICT (id) DO UPDATE SET
		  status = 'active'::gmc_connection_status,
		  account_status = 'warning',
		  warnings_count = 1,
		  updated_at = now()
	`, id("gmc:aurelia"), id("tenant:growth-collective"), id("store:aurelia"), dummyEnc); err != nil {
		return fmt.Errorf("insert gmc connection: %w", err)
	}
	return nil
}

func sha256Bytes(s string) []byte {
	// Deterministic 32-byte digest so re-running the seeder doesn't churn
	// invitation token_hash columns. Imports kept local to this file.
	h := newHash()
	_, _ = h.Write([]byte(s))
	return h.Sum(nil)
}

func getenv(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
