# TODO

Backlog for staging + production rollout. Suggested order is roughly
top-to-bottom: each item assumes the ones above it are done, but feel
free to reshuffle.

## Foundation

- [ ] **OPERATIONS.md decision record.** Lock in the staging+prod
  decisions before any of it gets built: topology choice (VPS+Caddy+
  systemd vs PaaS), trunk-based flow with auto-deploy `main` →
  staging and tag-gated prod, secret rotation policy for
  `SETTINGS_ENCRYPTION_KEY`, expand→ship→contract migration
  discipline, per-env Gumroad/Google/Postgres separation. Doc only.

- [ ] **GitHub Actions CI workflow** at `.github/workflows/test.yml`.
  Runs `go test ./...` against a Postgres service container + Mailhog,
  then the Playwright e2e (`xvfb-run -a node scripts/e2e-happy-path.js`),
  uploads coverage, gates PRs into `main`.

## Deploy artifacts

- [ ] **Caddyfile + systemd + provisioning.** Caddyfile (TLS
  termination, reverse proxy, `X-Forwarded-*` + `X-Request-ID`
  passthrough), systemd unit files for `bin/server`, `bin/worker`,
  `bin/scheduler` with `EnvironmentFile=` and `Restart=always`, and a
  `provision.sh` that bootstraps a fresh VPS (deploy user, Postgres,
  Caddy, app dir, secrets dir).

- [ ] **CI auto-deploy staging + tag-gated prod.** Two GitHub Actions
  jobs: (1) on push to `main` with green tests, SSH to staging, rsync
  binaries, run `migrate-up`, restart systemd units, hit `/readyz` to
  confirm. (2) on `v*` git tag, same dance against prod (manual tagging
  is the gate).

- [ ] **Trust Caddy-supplied headers.** Treat `X-Forwarded-For` /
  `X-Real-IP` as authoritative *only* when the request came through
  Caddy; propagate inbound `X-Request-ID` instead of generating a new
  one when present; surface the real client IP in `clientIP()`, the
  login limiter, and the impersonation log. Depends on the Caddyfile
  being in place.

## Per-env story

- [ ] **Per-env config + secret hygiene.** Separate Postgres database,
  separate `SETTINGS_ENCRYPTION_KEY`, separate Gumroad seller (or at
  minimum separate webhook secret + product permalinks), separate
  Google OAuth client with env-specific redirect URI. Document in
  `OPERATIONS.md` and reference the GitHub Actions secret names.

- [ ] **Provision real Google + Gumroad + AI.** Create the Google
  Cloud project (Content API + OAuth consent screen + redirect URIs
  for staging and prod registered), create the 5 Gumroad products
  with custom_fields + webhook subscriptions, set the AI API key.
  Drop the env vars in private `.env`s and re-run the existing demos
  against live services.

## Polish

- [ ] **Production hardening pass.** Rate limits on `/signup` and
  `/forgot-password` (extend the existing `LoginLimiter` pattern),
  tighter security headers (drop `unsafe-eval` if Alpine still allows
  it after refactor; HSTS preload), `/metrics` endpoint behind basic
  auth for Prometheus, structured-log shipping recommendation in
  `OPERATIONS.md`.

---

This file is generated from the in-session task list. The runtime
copy lives in the Claude Code session transcript at
`~/.claude/projects/-workspaces-shopify-gmc/<session>.jsonl`; this file
is the human-readable mirror that survives outside the session.
