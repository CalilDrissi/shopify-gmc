# Operations

What's running for `shopifygmc.com`, how to do everyday things, and what
you still own. If something here is wrong, the source files in `deploy/`
are the ground truth.

---

## 1. What's live

One Ubuntu 24.04 VPS at **`62.169.16.57`** (Contabo) runs both
environments + the mail stack + Cloudflare-fronted DNS.

| URL | What it is |
| --- | --- |
| <https://shopifygmc.com> | **Production** — the real app |
| <https://www.shopifygmc.com> | Same as above (alias) |
| <https://staging.shopifygmc.com> | **Staging** — same code, separate database |
| <https://shopifygmc.com/admin> | Platform admin (TOTP-gated) |
| <https://staging.shopifygmc.com/admin> | Platform admin for staging |
| <https://mail.shopifygmc.com> | Roundcube webmail |
| <https://shopifygmc.com/healthz> | Liveness probe (always 200) |
| <https://shopifygmc.com/readyz> | DB-readiness probe (200/503) |

Behind those URLs:

| Layer | Software | Notes |
| --- | --- | --- |
| TLS + reverse proxy | **Caddy** | Auto-issues Let's Encrypt certs for all four hostnames |
| App | **Go binaries** in `/opt/gmcauditor/{prod,staging}/bin/` | server / worker / scheduler per env |
| Data | **PostgreSQL 16** | `gmcauditor_prod`, `gmcauditor_staging` databases |
| Outbound mail | **Postfix** + **OpenDKIM** | DKIM-signs everything from `noreply@shopifygmc.com` |
| Inbound mail | **Postfix** virtual mailboxes + **Dovecot** | Maildirs at `/var/mail/vmail/<domain>/<local>/` |
| Webmail | **Roundcube** + **PHP-FPM** | Fronted by Caddy |

---

## 2. Logging in

### Platform admin

You have one super-admin account that works on both envs:

- **Email**: `admin@shopifygmc.com`
- **Password**: the 24-char random one printed when I provisioned (rotate
  it via Roundcube → Settings → Password as soon as you can; until then
  it's only in the chat transcript).
- **TOTP**: walked on first login. Use any authenticator (Google
  Authenticator / 1Password / Bitwarden / Authy) — point it at the QR
  code, type the 6-digit code.

Visit either:
- <https://shopifygmc.com/admin/login>
- <https://staging.shopifygmc.com/admin/login>

Once in, the sidebar nav has Dashboard / Tenants / Audits / Jobs /
Admins / Audit log / **GMC** / **Mail** / Settings.

### Webmail

<https://mail.shopifygmc.com> with `admin@shopifygmc.com` and the same
password. Roundcube settings:

| Field | Value |
| --- | --- |
| IMAP server | `mail.shopifygmc.com:993` (IMAPS, no STARTTLS) |
| SMTP server | `mail.shopifygmc.com:587` (STARTTLS) |
| Username | full address |
| Password | the Dovecot password |

Same settings work for any IMAP client (Apple Mail, Thunderbird, Outlook).

### SSH (for ops)

```bash
ssh -i <your-key> root@62.169.16.57
```

You should put your own SSH key in `/root/.ssh/authorized_keys` and then
**disable password auth** in `/etc/ssh/sshd_config`
(`PasswordAuthentication no`, `PermitRootLogin prohibit-password`) +
`systemctl restart ssh`. Right now there's a deploy key from the
Codespace but it'll vanish when that container dies; don't rely on it.

---

## 3. Daily operations

### Add a mailbox (UI)

- <https://shopifygmc.com/admin/mail>
- Fill the **Add mailbox** form. Leave password blank to get a generated
  one — it's shown **once** in the green banner.
- The mailbox is reachable immediately; no service restart needed.

### Add a mailbox (CLI, equivalent)

```bash
# Generated password
ssh root@62.169.16.57 mailbox add hello@shopifygmc.com

# Pick your own
ssh root@62.169.16.57 mailbox add jane@shopifygmc.com 'somethingsecret'

# Alias one address to another
ssh root@62.169.16.57 mailbox alias support@shopifygmc.com jane@shopifygmc.com

# Forward an alias to an external address
ssh root@62.169.16.57 mailbox alias bills@shopifygmc.com cal@gmail.com

# See everything
ssh root@62.169.16.57 mailbox list

# Rotate a password
ssh root@62.169.16.57 mailbox passwd jane@shopifygmc.com

# Delete (asks you to type the address to confirm)
ssh root@62.169.16.57 mailbox del jane@shopifygmc.com
```

### Read service logs

```bash
# Live tail
ssh root@62.169.16.57 'journalctl -u gmcauditor-prod-server -f'
ssh root@62.169.16.57 'journalctl -u gmcauditor-prod-worker -f'

# Last 50 lines for staging
ssh root@62.169.16.57 'journalctl -u gmcauditor-staging-server -n 50'

# Mail logs
ssh root@62.169.16.57 'tail -f /var/log/mail.log'

# Caddy access logs
ssh root@62.169.16.57 'tail -f /var/log/caddy/prod-access.log'
```

App logs are also on disk at `/var/log/gmcauditor/<env>/<svc>.log`.

### Restart services

```bash
# One service
ssh root@62.169.16.57 'systemctl restart gmcauditor-prod-server'

# All three services in one env
ssh root@62.169.16.57 'systemctl restart gmcauditor-prod.target'

# Both envs
ssh root@62.169.16.57 'systemctl restart gmcauditor-prod.target gmcauditor-staging.target'

# Caddy (after a Caddyfile edit)
ssh root@62.169.16.57 'systemctl reload caddy'

# Mail stack
ssh root@62.169.16.57 'systemctl restart postfix dovecot opendkim'
```

### Edit per-env configuration

Per-env env files live on the box (not in git):

```
/opt/gmcauditor/prod/env/app.env       # 0640 root:deploy
/opt/gmcauditor/staging/env/app.env
```

After editing, restart the affected env:

```bash
ssh root@62.169.16.57 'vi /opt/gmcauditor/prod/env/app.env && systemctl restart gmcauditor-prod.target'
```

### Health checks

```bash
curl -s https://shopifygmc.com/healthz   # → ok
curl -s https://shopifygmc.com/readyz    # → ready (200) or db unreachable (503)
curl -s https://staging.shopifygmc.com/healthz
```

---

## 4. Deploys

There's no CI yet — pushes go up by hand. From a local checkout of the
repo (with Go installed):

```bash
# 1. Build linux binaries
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/server    ./cmd/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/worker    ./cmd/worker
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/scheduler ./cmd/scheduler
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/migrate   ./cmd/migrate
make build-css

# 2. Stop services first if you're replacing the binary (text file busy)
ssh root@62.169.16.57 'systemctl stop gmcauditor-staging-server gmcauditor-prod-server'

# 3. Push artifacts (both envs at once)
for env in staging prod; do
  rsync -az build/      root@62.169.16.57:/opt/gmcauditor/$env/bin/
  rsync -az static/     root@62.169.16.57:/opt/gmcauditor/$env/static/
  rsync -az templates/  root@62.169.16.57:/opt/gmcauditor/$env/templates/
  rsync -az migrations/ root@62.169.16.57:/opt/gmcauditor/$env/migrations/
done
ssh root@62.169.16.57 'chown -R deploy:deploy /opt/gmcauditor'

# 4. Migrate (idempotent — runs only the new ones)
ssh root@62.169.16.57 '
  for env in staging prod; do
    cd /opt/gmcauditor/$env
    sudo -u deploy bash -c "set -a && source env/app.env && set +a && ./bin/migrate up"
  done
'

# 5. Restart everything
ssh root@62.169.16.57 'systemctl restart gmcauditor-staging.target gmcauditor-prod.target'

# 6. Sanity check
curl -s -w "\nHTTP %{http_code}\n" https://shopifygmc.com/readyz
curl -s -w "\nHTTP %{http_code}\n" https://staging.shopifygmc.com/readyz
```

The CI auto-deploy job (TODO #50/#53) will collapse this to a `git push`.

---

## 5. Credentials inventory

Where each secret lives + how to rotate it.

| Credential | Lives in | Rotate by |
| --- | --- | --- |
| **Server root password** | Contabo customer panel | Change in panel + run `passwd` over SSH; better, disable password auth entirely (see §2) |
| **SSH** | `~/.ssh/authorized_keys` on the box | `ssh-keygen` locally → `ssh-copy-id` to box → remove the codespace key from `authorized_keys` |
| **Cloudflare API token** | Cloudflare dashboard → My Profile → API Tokens | Revoke + create a new one with Zone:DNS:Edit on `shopifygmc.com` |
| **Postgres role passwords** | `/opt/gmcauditor/<env>/env/app.env` line `DATABASE_URL` | `sudo -u postgres psql -c "ALTER ROLE gmc_prod PASSWORD 'newpw'"` then update env + restart |
| **`APP_SECRET`** | `/opt/gmcauditor/<env>/env/app.env` | Generate new with `openssl rand -hex 32`, swap, restart. **Invalidates all CSRF tokens + signed unsubscribe links.** |
| **`SETTINGS_ENCRYPTION_KEY`** | `/opt/gmcauditor/<env>/env/app.env` | **Don't rotate without a re-encrypt step.** Encrypted refresh tokens in `store_gmc_connections` are unreadable after rotation; users have to re-consent to GMC. |
| **Platform admin password** | Postgres `users.password_hash` | Roundcube → Settings → Password (rotates the IMAP password) **and** Account → Change password (rotates the app login). They're separate. |
| **Dovecot mailbox passwords** | `/etc/dovecot/users` (ARGON2ID) | `mailbox passwd EMAIL` (CLI) or rotate from Roundcube |
| **Gumroad webhook secret** | `/opt/gmcauditor/<env>/env/app.env` `GUMROAD_WEBHOOK_SECRET` | Rotate in Gumroad → update env → restart server |
| **Google OAuth client secret** | `/opt/gmcauditor/<env>/env/app.env` `GOOGLE_OAUTH_CLIENT_SECRET` | Rotate in Cloud Console → update env → restart |
| **AI API key** | platform_settings table (encrypted via SETTINGS_ENCRYPTION_KEY) | <https://shopifygmc.com/admin/settings> |

---

## 6. What you still own (priority order)

1. **PTR record `62.169.16.57 → mail.shopifygmc.com`** in Contabo's
   panel. Without this, Gmail aggressively spam-folders or rejects.
   Once set, Gmail still takes a couple weeks to fully trust the IP.

2. **Rotate the credentials in this transcript** — root SSH password +
   Cloudflare token + admin@shopifygmc.com password. None of them
   should keep living in chat history.

3. **Wire integration credentials** in
   `/opt/gmcauditor/<env>/env/app.env`:
   - `GUMROAD_*` (5 product permalinks + webhook secret) — until set,
     pricing buttons are visibly disabled.
   - `GOOGLE_OAUTH_CLIENT_ID/_SECRET` + register
     `https://shopifygmc.com/oauth/google/callback` and the staging
     equivalent in Cloud Console.
   - `OPENAI_API_KEY` (or whichever provider) via
     <https://shopifygmc.com/admin/settings>.

4. **First user signup**. Sign up at <https://shopifygmc.com/signup> —
   verify email arrives, click link, you can use the workspace.

5. **Backups**. Postgres has none yet. Two reasonable options:
   - Nightly `pg_dump` cron pushed to Backblaze B2 / S3 (~$1/mo).
   - Move the database to a managed Postgres provider (Neon, Supabase,
     RDS) and just point `DATABASE_URL` at it.

---

## 7. Troubleshooting

### "Service won't start"

```bash
ssh root@62.169.16.57 '
  systemctl status gmcauditor-prod-server --no-pager
  journalctl -u gmcauditor-prod-server -n 50 --no-pager
'
```

Check the log for `Error: …` or `panic:`.

### "Outbound mail not arriving"

```bash
ssh root@62.169.16.57 'tail -50 /var/log/mail.log'
```

Look for `dsn=` codes:
- `2.x.x` + `status=sent` → delivered to recipient's MX
- `4.x.x` + `status=deferred` → soft-fail, will retry
- `5.x.x` + `status=bounced` → hard-fail; the receiving server
  refused (check the message — usually rate limit, spam score, or
  unknown recipient)

If everything's `2.0.0 sent` but the user can't see it: check their
spam folder. PTR + warm-up time is usually the issue.

### "/readyz returns 503"

The app can't reach Postgres. Check:

```bash
ssh root@62.169.16.57 '
  systemctl status postgresql --no-pager
  sudo -u postgres psql -c "SELECT 1"
  sudo -u deploy bash -c "set -a && source /opt/gmcauditor/prod/env/app.env && set +a && /opt/gmcauditor/prod/bin/migrate version"
'
```

### "Webmail won't load"

```bash
ssh root@62.169.16.57 '
  systemctl status php*-fpm --no-pager
  systemctl status caddy --no-pager
  tail -20 /var/log/caddy/webmail-access.log
'
```

### "DNS change not visible"

Cloudflare has a 5-min TTL on our records. If a record looks wrong:

```bash
# Check what Cloudflare actually serves
dig +short @1.1.1.1 mail.shopifygmc.com
# vs what you set
curl -sS -H "Authorization: Bearer $CF_TOKEN" \
  'https://api.cloudflare.com/client/v4/zones/2bc0a81fd68f541acc2bcaab9fab673b/dns_records?name=mail.shopifygmc.com'
```

---

## 8. Architecture (brief)

Code under `cmd/` builds five binaries; `internal/` holds private
packages; `deploy/` holds the bootstrap scripts.

```
cmd/server      HTTP server (Caddy → :8080 prod / :8081 staging)
cmd/worker      Audit job consumer (FOR UPDATE SKIP LOCKED)
cmd/scheduler   Tick loop: schedules audits + GMC background refresh
cmd/seed        CLI: `seed all` for fixture data, `grant-admin` to elevate
cmd/migrate     golang-migrate wrapper

internal/audit       Pipeline + 20 crawler checks + 9 GMC checks
internal/audit/differ   Per-audit diff vs previous succeeded audit
internal/billing     Gumroad webhook (HMAC verify + dispatch)
internal/gmc         Google OAuth + Content API client
internal/jobs        Postgres-backed job queue
internal/monitoring  Alert dispatcher (rate-limited 1/24h except critical)
internal/scheduler   Audit + GMC refresh loops
internal/web         HTTP handlers + middleware

deploy/provision.sh  Fresh-VPS bootstrap (Postgres, Caddy, deploy user)
deploy/units.sh      6 systemd units + Caddyfile
deploy/smtp.sh       Postfix + OpenDKIM (outbound + DKIM signing)
deploy/webmail.sh    Inbound + Dovecot + Roundcube
deploy/mailbox       CLI helper for managing virtual mailboxes
```

Tests:

```bash
make test                                 # all Go tests
xvfb-run -a node scripts/e2e-happy-path.js   # full Playwright e2e
```

---

## 9. Backlog

See `TODO.md` at the repo root. The big ones still pending:

1. **GitHub Actions CI** — auto-test PRs into main.
2. **Auto-deploy** — staging on every green push to main; prod on
   `git tag v*`. Replaces the manual rsync above.
3. **Per-env config + secret hygiene** — capture the per-env
   secrets in a vault (1Password, sops, or just GitHub Actions
   secrets) instead of editing env files by hand.
4. **Production hardening** — rate-limit `/signup` and
   `/forgot-password`, security-headers tightening, `/metrics` for
   Prometheus.
5. **Trust Caddy-supplied headers** — read `X-Forwarded-For` /
   `X-Real-IP` so login limiter and impersonation log see the real
   client IP.

If you want to pick one of these up, say which and I'll execute.
