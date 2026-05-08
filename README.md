# gmcauditor

Multi-tenant SaaS that audits Shopify stores against Google Merchant Center
(GMC) compliance rules and proposes AI-generated fixes.

> **Operating the live deployment?** Start here:
> [`guide/`](./guide/) — credentials, mail management, deploys,
> troubleshooting. This README is for developers reading the codebase.

- **Crawler-side checks** — 20 deterministic rules that read HTML/JSON-LD
  (HTTPS, structured data, policy pages, schema completeness, image alts,
  canonical tags, etc.) and produce one issue per failure.
- **GMC-native checks** — 9 rules driven by the Content API (account
  suspensions, item-level disapprovals, feed errors, image policy
  violations) that run only when a store is connected.
- **AI-suggested fixes** — OpenAI-compatible chat-completions, one
  paragraph per issue, with per-audit budget caps and graceful fallback.
- **Monitoring** — scheduler claims due stores via `FOR UPDATE SKIP LOCKED`,
  worker runs the audit pipeline, differ inserts an `audit_diffs` row, and
  the alert dispatcher sends per-trigger emails (rate-limited 1/24h
  except `new_critical` and `audit_failed`).
- **Billing** — Gumroad webhooks signed with HMAC-SHA256 flip the tenant's
  plan and reconcile cancellations / refunds.

## Stack

- **Go 1.23+** — stdlib `net/http`, `html/template`, `log/slog`. No web
  framework.
- **PostgreSQL 16** — pgx/v5 pool, golang-migrate for schema, `BYPASSRLS`
  on the app role + tenant-scoped policies on every multi-tenant table.
- **HTMX + Alpine.js** — vendored under `static/js/`. Sass with BEM,
  compiled by `dart-sass` to `static/css/main.css`.
- **Gumroad** for licensing/payments.
- **Mailhog** for local email capture.

## Repository layout

```
cmd/         entry points: server, worker, scheduler, seed, migrate
internal/    private packages
  ai/        OpenAI-compatible client + mock + prompts
  audit/     pipeline + check registry + differ
  audit/checks/  20 crawler rules + 9 GMC rules
  auth/      argon2id, sessions, cookies, CSRF, TOTP
  billing/   Gumroad: signature verify, parse, dispatch
  crawler/   robots/sitemap-aware page fetcher
  gmc/       Google OAuth + Content API client
  jobs/      pg-backed audit_jobs queue + worker
  mailer/    SMTP + html/template emails
  monitoring/  alert dispatcher + 24h rate limiter
  scheduler/ tick loop + GMC refresher
  settings/  AES-256-GCM cipher + platform_settings repo
  store/     pgx repositories
  web/       handlers, middleware, plan gate
templates/   layouts, pages, partials
styles/      Sass sources → static/css
migrations/  golang-migrate SQL files
scripts/     Playwright flows (signup, audit, billing, e2e-happy-path, ...)
```

## Setup

### 1. Install prerequisites

- **Go 1.23+** — `go version`.
- **Docker** — used for Postgres + Mailhog.
- **Node 20+ + npm** — only for compiling Sass + running Playwright
  flows. `make build-css` runs `npm install` for you.
- **dart-sass** — pulled in via `npm install` (the `sass` package).

### 2. Bring up Postgres + Mailhog

```bash
make docker-up
```

That brings up `gmcauditor-postgres` (port 5432, user `gmc`/password `gmc`,
database `gmcauditor`) and Mailhog (SMTP on 1025, UI on
http://localhost:8025).

### 3. Configure environment

Copy the example file and edit. The required fields for the local dev
loop are at the top; integration sections (Google, Gumroad, AI) are
described later in this README.

```bash
cp .env.example .env
```

Minimum:

```
DATABASE_URL=postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable
APP_BASE_URL=http://localhost:8080
APP_SECRET=<a-long-random-string>
SETTINGS_ENCRYPTION_KEY=<base64 32-byte key>
SMTP_HOST=localhost
SMTP_PORT=1025
SMTP_FROM=noreply@gmcauditor.local
```

Generate `SETTINGS_ENCRYPTION_KEY`:

```bash
openssl rand -base64 32
```

### 4. Migrate the schema

```bash
make migrate-up
```

### 5. Seed development data (optional)

```bash
SEED_ADMIN_EMAIL=admin@gmcauditor.local \
SEED_ADMIN_PASSWORD=super-strong-pass-2026 \
go run ./cmd/seed all
```

This is idempotent — re-running upserts the same fixture rows. Creates:

- 1 platform super_admin (from the env vars above)
- 2 tenants: **Sarah's Shop** (Starter) and **Growth Collective** (Agency)
- 4 user accounts with mixed memberships (all sharing password
  `super-strong-pass-2026`)
- 6 stores, 4 succeeded audits with realistic content (scores 32 / 67 / 78
  / 91), 1 failed, 1 queued
- 2 pending invitations, 3 historical purchases, 1 GMC connection
- Monitoring enabled on 2 stores

### 6. Compile assets and run

```bash
make build-css
make build         # → bin/server, bin/worker, bin/scheduler, bin/seed
./bin/server &
./bin/worker -mode=worker &
./bin/scheduler -mode=scheduler &
```

App: <http://localhost:8080> · Mailhog: <http://localhost:8025>.

### Health probes

- `GET /healthz` — always 200; the process is up.
- `GET /readyz` — 200 if the DB pool can `SELECT 1`, else 503.

Use these in your orchestrator's liveness / readiness probes. The
request logger silences both paths.

## Integrations

### Google Merchant Center (OAuth + Content API)

The GMC integration uses standard OAuth 2.0 against Google's Content API
for Shopping. The server stores an AES-256-GCM-encrypted refresh token;
access tokens are minted in memory per call and never written to disk.

#### One-time Google Cloud setup

1. **Create a project** at <https://console.cloud.google.com>. Name it
   anything.
2. **Enable the Content API for Shopping** under APIs & Services →
   Library.
3. **Configure the OAuth consent screen** under APIs & Services →
   OAuth consent screen.
   - User type: **External** for most users; **Internal** if you only
     ever connect Workspace accounts.
   - Add the scope `https://www.googleapis.com/auth/content` (the only
     scope we ask for).
   - Add **Test users** for every Google account that will demo the
     connect flow while the app is in *Testing* mode.
4. **Create OAuth client credentials** under APIs & Services →
   Credentials → Create Credentials → OAuth client ID.
   - Application type: **Web application**.
   - Authorised redirect URIs: add **exactly** the URL that matches
     `GOOGLE_OAUTH_REDIRECT_URL` below — Google rejects wildcards. For
     local dev that's `http://localhost:8080/oauth/google/callback`.
   - Copy the Client ID and Client Secret.

#### Server configuration

```
GOOGLE_OAUTH_CLIENT_ID=...apps.googleusercontent.com
GOOGLE_OAUTH_CLIENT_SECRET=...
GOOGLE_OAUTH_REDIRECT_URL=http://localhost:8080/oauth/google/callback
```

The store-detail page now shows a **Connect Google Merchant Center**
button (owner-only, blocked while a platform admin is impersonating).

#### What the connect flow does

1. User clicks **Connect Google Merchant Center** on the store page.
2. We HMAC-sign a small `state` payload (session id, tenant id, store id,
   user id, nonce, issued-at, 10-minute TTL) and redirect to Google with
   `scope=auth/content`, `access_type=offline`, `prompt=consent`.
3. Google redirects back to the **fixed** `{BASE_URL}/oauth/google/callback`
   — Google's allowlist doesn't accept wildcards, so per-store callback
   URLs aren't possible at scale. `state` carries the store id instead.
   We verify the HMAC signature and that the cookie session still matches.
4. We exchange the code for refresh + access tokens, then call
   `accounts/authinfo` to discover which Merchant Center accounts the
   user manages. Auto-link if exactly one, picker UI if multiple.
5. The refresh token is encrypted with `SETTINGS_ENCRYPTION_KEY` and
   stored in `store_gmc_connections.refresh_token_encrypted`.
   `ConnectionStore.SupplierFor(connID)` decrypts only into a local
   `[]byte`, exchanges for an access token, zeroes the plaintext, caches
   the access token in process memory for ~5 minutes.
6. **Disconnect** revokes the refresh token at
   `oauth2.googleapis.com/revoke` then nulls the encrypted blob.

Errors:

- **No refresh token returned** — Google only issues a refresh token on
  the first consent. Re-consent via
  https://myaccount.google.com/permissions when needed.
- **401 from the Content API** — flips the connection to `revoked` and
  emails the tenant's owners.
- **429 from the Content API** — exponential backoff capped at 1 hour,
  honouring `Retry-After`. After 4 retries the call returns
  `gmc.ErrRateLimited` so the caller can requeue.

### Gumroad (subscriptions + one-time charges)

Five products live in Gumroad's dashboard. We never call Gumroad's API —
purchases arrive as webhooks signed with HMAC-SHA256.

#### One-time Gumroad setup

1. **Create five products** at <https://gumroad.com/products>. Set each
   product's *custom permalink* and copy it to the matching env var.

   | Permalink   | Title              | Type                  | Price |
   | ----------- | ------------------ | --------------------- | ----- |
   | `gmc-starter` | Starter          | Subscription, monthly | $19   |
   | `gmc-growth`  | Growth           | Subscription, monthly | $49   |
   | `gmc-agency`  | Agency           | Subscription, monthly | $199  |
   | `gmc-rescue`  | Rescue Audit     | Single purchase       | $99   |
   | `gmc-dfy`     | DFY Reinstatement| Single purchase       | $499  |

2. **Custom fields** — on each product, add two:
   - `tenant_id` (hidden, prefilled from URL) — maps the sale to a
     workspace.
   - `user_email` (visible, optional) — informational.

3. **Post-purchase redirect** — for every product set the redirect to
   `{BASE_URL}/t/PLACEHOLDER/billing?gumroad_return=1`. The buyer's
   browser polls a "processing payment" page until our webhook lands.

4. **Webhook URL** — Gumroad → Settings → Advanced → Resource
   subscriptions. Add `sale`, `subscription_updated`,
   `subscription_cancelled`, `subscription_ended`, `refund` subscriptions
   pointing at `{BASE_URL}/webhooks/gumroad`. Copy the shared HMAC
   secret to `GUMROAD_WEBHOOK_SECRET`.

#### Server configuration

```
GUMROAD_WEBHOOK_SECRET=<from-gumroad>
GUMROAD_PRODUCT_STARTER=gmc-starter
GUMROAD_PRODUCT_GROWTH=gmc-growth
GUMROAD_PRODUCT_AGENCY=gmc-agency
GUMROAD_PRODUCT_RESCUE=gmc-rescue
GUMROAD_PRODUCT_DFY=gmc-dfy
OPERATOR_EMAIL=ops@yourdomain
```

#### What the webhook handler does

1. Reads the raw POST body.
2. Verifies `X-Gumroad-Signature` against `GUMROAD_WEBHOOK_SECRET`
   (HMAC-SHA256 of body, hex-encoded). Failure → 401, no DB write.
3. Parses the form via `billing.ParseForm` — extracts `sale_id`,
   `product_permalink`, `tenant_id`, refund/subscription fields.
4. Inserts `gumroad_webhook_events` row with
   `ON CONFLICT (event_type, sale_id) DO NOTHING` — Gumroad retries are
   idempotent.
5. Dispatches in the same tx (sale → upsert purchase + flip plan;
   refund → mark refunded + downgrade; subscription_cancelled →
   schedule downgrade to free at period end via `pending_plan` /
   `pending_plan_at`; etc.).
6. Marks `processed_at` on the event row, commits, returns 200.

### AI (OpenAI-compatible chat completions)

Optional. When `ai_api_key` is set in `platform_settings` (or the
worker's environment via the platform-admin settings page), the worker
uses the real client; otherwise it falls back to a deterministic
mock. The audit pipeline calls the AI client only inside the *Enrich*
and *Summarize* stages and always degrades gracefully on error.

## Plan-quota matrix

| Plan       | Stores | Audits/mo | Members | Monitoring         | GMC connections | White-label PDF |
| ---------- | ------ | --------- | ------- | ------------------ | --------------- | --------------- |
| Free       | 1      | 1         | 3       | —                  | 0               | no              |
| Starter    | 3      | 10        | 5       | weekly             | 1               | no              |
| Growth     | 10     | 50        | 15      | daily              | 3               | no              |
| Agency     | 50     | 500       | 50      | daily, priority    | 50              | yes             |
| Enterprise | 500    | 5000      | 500     | daily, priority    | 500             | yes             |

The 5 mutation endpoints — store create, audit enqueue, monitoring
enable, member invite, GMC connect — call `EnforcePlanLimit` and render a
402 page with an inline upgrade CTA when a tenant hits its quota.

## Operations

### Graceful shutdown

Server, worker, and scheduler all listen for SIGTERM/SIGINT. Each runs a
30-second drain:

- **Server** — stops accepting new requests, waits up to 30s for
  in-flight handlers (HTMX polls, audit POSTs, etc.).
- **Worker** — stops claiming new jobs, waits up to 30s for in-flight
  audit pipelines to commit. Times out logged at WARN.
- **Scheduler** — exits the tick loop on the next iteration; waits up to
  30s for the GMC background refresher to return.

### Structured logs

`slog` with JSON output. Every `http_request` line carries
`request_id` plus whichever identity fields the middleware chain
populated for that request:

- `user_id` — set by `RequireUser`
- `tenant_id` — set by `LoadTenantBySlug`
- `platform_admin_id` — set when the request runs under an admin
  impersonation session

Handlers can opt into the same enrichment for non-request-level logs
via `web.ContextLogger(h.Logger, r.Context())`.

Secrets (refresh tokens, AI API keys, signing secrets, Gumroad webhook
secret) are never logged. Webhook bodies are stored in the
`gumroad_webhook_events.payload` jsonb column under RLS scope; access
tokens are minted in memory and never persisted.

## Make targets

```
make help            list available targets
make docker-up       postgres + mailhog
make docker-down     stop them
make migrate-up      apply migrations
make migrate-down    rollback one
make build           bin/server, bin/worker, bin/scheduler, bin/seed
make build-css       compile Sass
make dev-css         watch mode
make run             go run ./cmd/server
make test            go test ./...
make tidy            go mod tidy
```

## Tests

```bash
DATABASE_URL=postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable go test ./...
```

End-to-end Playwright flow (signup → audit → monitoring → diff → alert
email):

```bash
xvfb-run -a node scripts/e2e-happy-path.js
```

Other Playwright flows in `scripts/`:

- `flow-billing.js` — pricing/billing pages + signed Gumroad webhook
  test (sale, replay, bad signature, refund, plan-limit 402).
- `flow-monitoring-full.js` — monitoring card, scheduled audit, alert
  email.
- `flow-gmc-pipeline.js` — GMC connect card, tabbed audit report,
  /admin/gmc.
- `flow-admin.js` — platform admin TOTP enrol + tenant impersonation.

All flows assume the binaries are built (`make build`) and Postgres +
Mailhog are up (`make docker-up`).
