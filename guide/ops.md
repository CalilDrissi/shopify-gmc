# Operations

Daily commands, deploys, and what to do when something breaks.

## Health checks

```bash
curl -s https://shopifygmc.com/healthz   # → "ok"     (process alive)
curl -s https://shopifygmc.com/readyz    # → "ready"  (DB reachable)
```

If `/readyz` returns 503, Postgres is unreachable from the app. Jump
to **[Troubleshooting](#troubleshooting)** below.

## Reading logs

```bash
# Live tail of one service
ssh root@62.169.16.57 'journalctl -u gmcauditor-prod-server -f'

# Last 100 lines of the worker
ssh root@62.169.16.57 'journalctl -u gmcauditor-prod-worker -n 100'

# Mail logs
ssh root@62.169.16.57 'tail -f /var/log/mail.log'

# Caddy access logs
ssh root@62.169.16.57 'tail -f /var/log/caddy/prod-access.log'
ssh root@62.169.16.57 'tail -f /var/log/caddy/webmail-access.log'
```

App logs also land on disk at `/var/log/gmcauditor/prod/<svc>.log`.

## Restarting services

```bash
# One service
ssh root@62.169.16.57 'systemctl restart gmcauditor-prod-server'

# All three services (server + worker + scheduler)
ssh root@62.169.16.57 'systemctl restart gmcauditor-prod.target'

# Caddy after a Caddyfile edit
ssh root@62.169.16.57 'systemctl reload caddy'

# Mail stack
ssh root@62.169.16.57 'systemctl restart postfix dovecot opendkim'
```

Each app service has a 30-second graceful drain on SIGTERM — in-flight
HTTP requests and audit jobs complete before the process exits.

## Editing configuration

The env file lives on the box (not in git, by design):

```
/opt/gmcauditor/prod/env/app.env
```

After editing, restart:

```bash
ssh root@62.169.16.57 'vi /opt/gmcauditor/prod/env/app.env && systemctl restart gmcauditor-prod.target'
```

Common knobs in `app.env`: `SMTP_*`, `GOOGLE_OAUTH_*`,
`GUMROAD_*`, `OPERATOR_EMAIL`. The file's mode is `0640 root:deploy`
so the deploy user can read but not edit.

## Deploys

There's no CI auto-deploy yet — pushes are manual. From a local
checkout of the repo:

```bash
# 1. Build linux binaries
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/server    ./cmd/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/worker    ./cmd/worker
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/scheduler ./cmd/scheduler
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/migrate   ./cmd/migrate
make build-css

# 2. Stop the server so the in-use binary can be replaced
ssh root@62.169.16.57 'systemctl stop gmcauditor-prod-server'

# 3. Push artifacts
rsync -az build/      root@62.169.16.57:/opt/gmcauditor/prod/bin/
rsync -az static/     root@62.169.16.57:/opt/gmcauditor/prod/static/
rsync -az templates/  root@62.169.16.57:/opt/gmcauditor/prod/templates/
rsync -az migrations/ root@62.169.16.57:/opt/gmcauditor/prod/migrations/
ssh root@62.169.16.57 'chown -R deploy:deploy /opt/gmcauditor'

# 4. Migrate (idempotent — only runs new migrations)
ssh root@62.169.16.57 'cd /opt/gmcauditor/prod && sudo -u deploy bash -c "set -a && source env/app.env && set +a && ./bin/migrate up"'

# 5. Restart everything
ssh root@62.169.16.57 'systemctl restart gmcauditor-prod.target'

# 6. Sanity check
curl -s -w "\nHTTP %{http_code}\n" https://shopifygmc.com/readyz
```

The "GitHub Actions auto-deploy" task in [`../TODO.md`](../TODO.md)
will collapse this to a `git push`.

## Troubleshooting

### Service won't start

```bash
ssh root@62.169.16.57 '
  systemctl status gmcauditor-prod-server --no-pager
  journalctl -u gmcauditor-prod-server -n 50 --no-pager
'
```

Look for `Error: …` or `panic:` in the output. The most common cause
is a typo in `app.env` (missing `=`, unmatched quotes).

### `/readyz` returns 503

The app can't reach Postgres. Check:

```bash
ssh root@62.169.16.57 '
  systemctl status postgresql --no-pager
  sudo -u postgres psql -c "SELECT 1"
'
```

If Postgres is up but the app still 503s, the env's `DATABASE_URL`
is wrong — check `app.env`.

### Outbound mail not arriving

```bash
ssh root@62.169.16.57 'tail -50 /var/log/mail.log'
```

Look for `dsn=` codes:

| Code prefix | Meaning |
| --- | --- |
| `dsn=2.x.x` + `status=sent` | Delivered to recipient's MX. If user can't see it, check spam. |
| `dsn=4.x.x` + `status=deferred` | Soft-fail; Postfix will retry. |
| `dsn=5.x.x` + `status=bounced` | Hard-fail; receiving server refused. |

Most "user can't see verify email" issues are about spam scoring,
not delivery. The fix is **PTR record** (see
[`credentials.md`](./credentials.md#contabo-ptr-record)) and
warm-up time (Gmail trusts new IPs more once they've sent
non-bounce volume for ~2 weeks).

### Webmail won't load

```bash
ssh root@62.169.16.57 '
  systemctl status caddy --no-pager
  systemctl status php8.3-fpm --no-pager
  tail -20 /var/log/caddy/webmail-access.log
'
```

If PHP-FPM is dead, `systemctl restart php*-fpm`.

### DNS change not visible

Cloudflare TTL on our records is 5 minutes. If a record looks wrong:

```bash
# What Cloudflare actually serves
dig +short @1.1.1.1 mail.shopifygmc.com   # (install bind9-dnsutils if dig isn't on the box)

# Compare against what's set
curl -sS -H "Authorization: Bearer YOUR_CF_TOKEN" \
  'https://api.cloudflare.com/client/v4/zones/2bc0a81fd68f541acc2bcaab9fab673b/dns_records?name=mail.shopifygmc.com' \
  | jq '.result[]'
```

### Disk filling up

```bash
ssh root@62.169.16.57 'df -h / && du -sh /var/log /var/mail /var/lib/postgresql /opt/gmcauditor'
```

Common culprits: Caddy's access logs (configured to roll at 50MB,
keep 7 — but if you've been live for a while, maybe more), Postgres
WAL files, large user mailboxes.

### Backups

Nightly `pg_dump` cron writes prod dumps to
`/var/backups/gmcauditor/prod/`, keeping 7 days. Restore with:

```bash
sudo -u postgres pg_restore --clean --if-exists --no-owner \
  -d "$DATABASE_URL" /var/backups/gmcauditor/prod/<YYYY-MM-DD_HH-MM>.dump
```

Off-box copy isn't wired yet — a disk failure on the box loses the
backups too. Item #54 in [`../TODO.md`](../TODO.md) covers wiring an
off-box destination.
