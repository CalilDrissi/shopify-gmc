# gmcauditor

Multi-tenant SaaS that audits Shopify stores against Google Merchant Center
(GMC) compliance rules and proposes AI-generated fixes.

## Stack

- Go 1.23+ (stdlib `net/http`, `html/template`)
- PostgreSQL 16
- HTMX + Alpine.js, Sass with BEM
- Gumroad for licensing/payments
- Caddy as the production edge proxy
- Mailhog for local email capture

## Layout

```
cmd/         entry points: server, worker, scheduler, seed
internal/    private packages (auth, billing, db, web, shopify, gmc, ai, ...)
templates/   html/template files (layouts, pages, partials)
styles/      Sass sources compiled to static/css
static/      assets served at /static
migrations/  golang-migrate SQL files
testdata/    fixtures
```

## Getting started

```bash
cp .env.example .env
make docker-up
make migrate-up
make sass-build
make run
```

Mailhog UI: http://localhost:8025

## Make targets

Run `make help` for the full list.
