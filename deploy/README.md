# Deploy artifacts

Files used to bring `shopifygmc.com` (prod) and `staging.shopifygmc.com`
(staging) up on a single Ubuntu 24.04 VPS, both fronted by Caddy on the
same box.

## Files

- **`provision.sh`** — runs once on a fresh VPS as root. Installs
  Postgres, Caddy, and the system packages we need; creates the
  `deploy` user, the per-env directory tree under `/opt/gmcauditor/`,
  the per-env Postgres roles + databases (with `BYPASSRLS` since RLS
  policies on every multi-tenant table mean the app role needs to
  bypass them to run cross-tenant work in the worker), and a baseline
  ufw config (allow 22/80/443, deny everything else).
  
  Replace the two `*_DB_PW_PLACEHOLDER` strings with real per-env
  passwords before running. Generate with:
  ```bash
  openssl rand -base64 24 | tr -d '/+=' | cut -c1-24
  ```

- **`units.sh`** — installs 6 systemd units (server / worker /
  scheduler × staging / prod) plus the Caddyfile and starts
  everything. Idempotent: re-running re-templates the unit files,
  reloads systemd, and `reload`s Caddy.

  Each unit runs as `deploy`, sources its `EnvironmentFile=` from
  `/opt/gmcauditor/$env/env/app.env`, logs to
  `/var/log/gmcauditor/$env/$svc.log`, and sets the standard
  `NoNewPrivileges` / `ProtectSystem=strict` / `PrivateTmp` hardening
  flags. `TimeoutStopSec=35` gives the 30s in-app drain a 5s margin
  before SIGKILL.

- **`smtp.sh`** — installs Postfix (outbound MTA, listening on
  loopback only) + OpenDKIM (signing milter on `localhost:8891`),
  generates a 2048-bit DKIM key for selector `mail`, and writes a
  hardened `main.cf` with TLS-may + DKIM milter wired in. After
  install, the script prints the DKIM public key in BIND format —
  flatten to a single quoted string and add three TXT records:

  - `shopifygmc.com` → SPF `v=spf1 ip4:62.169.16.57 -all`
  - `mail._domainkey.shopifygmc.com` → DKIM
  - `_dmarc.shopifygmc.com` → DMARC
    `v=DMARC1; p=none; rua=mailto:dmarc@shopifygmc.com; ruf=mailto:dmarc@shopifygmc.com; fo=1`

  Both env's `app.env` should set `SMTP_HOST=localhost` `SMTP_PORT=25`
  `SMTP_FROM=noreply@shopifygmc.com`. Sending from a subdomain like
  `@staging.shopifygmc.com` won't be DKIM-signed unless you add a
  KeyTable+SigningTable for that subdomain — simplest is to keep both
  envs sending from the apex `From:`.

  **Reverse DNS (PTR) for the box's IPv4 → `mail.shopifygmc.com`** has
  to be set on the VPS provider's side, NOT in Cloudflare. Until then
  Gmail flags outbound as suspicious. On Contabo this lives in the
  customer panel under "Reverse DNS" for the VPS.

## Layout on the box

```
/opt/gmcauditor/
  staging/
    bin/{server,worker,scheduler,seed,migrate}
    env/app.env             # 0640 root:deploy
    static/  templates/  migrations/  styles/
  prod/                     # mirror of staging
/var/log/gmcauditor/{staging,prod}/{server,worker,scheduler}.log
/etc/systemd/system/
  gmcauditor-{staging,prod}-{server,worker,scheduler}.service
  gmcauditor-{staging,prod}.target
/etc/caddy/Caddyfile        # 2 vhosts — prod + www, staging
```

## Bootstrap from scratch

```bash
# On the dev/CI machine, build everything for linux/amd64:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/server    ./cmd/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/worker    ./cmd/worker
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/scheduler ./cmd/scheduler
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/seed      ./cmd/seed
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/migrate   ./cmd/migrate
make build-css

# Push provision script + run it:
scp deploy/provision.sh root@$HOST:/root/
ssh root@$HOST 'bash /root/provision.sh'

# Push artifacts to both envs:
for env in staging prod; do
  rsync -az build/      root@$HOST:/opt/gmcauditor/$env/bin/
  rsync -az static/     root@$HOST:/opt/gmcauditor/$env/static/
  rsync -az templates/  root@$HOST:/opt/gmcauditor/$env/templates/
  rsync -az migrations/ root@$HOST:/opt/gmcauditor/$env/migrations/
done
ssh root@$HOST 'chown -R deploy:deploy /opt/gmcauditor'

# Generate per-env app.env files (see provision.sh comments + the
# README.md `Setup` section for the full env-var list), then push:
scp env.staging root@$HOST:/opt/gmcauditor/staging/env/app.env
scp env.prod    root@$HOST:/opt/gmcauditor/prod/env/app.env
ssh root@$HOST 'chown root:root /opt/gmcauditor/*/env/app.env && chgrp deploy /opt/gmcauditor/*/env/app.env && chmod 0640 /opt/gmcauditor/*/env/app.env'

# Migrate both DBs:
ssh root@$HOST '
  for env in staging prod; do
    cd /opt/gmcauditor/$env
    sudo -u deploy bash -c "set -a && source env/app.env && set +a && ./bin/migrate up"
  done
'

# Install + start services:
scp deploy/units.sh root@$HOST:/root/
ssh root@$HOST 'bash /root/units.sh'

# Verify:
curl https://shopifygmc.com/healthz          # ok
curl https://shopifygmc.com/readyz           # ready
curl https://staging.shopifygmc.com/healthz  # ok
```

## Operations

```bash
# Tail logs
journalctl -u gmcauditor-prod-server -f
journalctl -u gmcauditor-staging-worker -f

# Restart one service
systemctl restart gmcauditor-prod-server

# Restart everything in an env
systemctl restart gmcauditor-prod.target

# Reload Caddy after a Caddyfile edit
systemctl reload caddy
```

## DNS

- Records live in Cloudflare. Three A records (TTL 300, proxy off so
  Caddy can do Let's Encrypt HTTP-01):
  - `shopifygmc.com`         → 62.169.16.57
  - `www.shopifygmc.com`     → 62.169.16.57
  - `staging.shopifygmc.com` → 62.169.16.57
- If you flip Cloudflare proxy on, switch Caddy to Cloudflare-issued
  origin certs (or use the DNS-01 challenge with a Zone:DNS:Edit token
  scoped only to this zone).

## What's NOT in this artifact set

- A CI deploy job that invokes the bootstrap above on push. See the
  `CI auto-deploy staging + tag-gated prod` task in `TODO.md`.
- The per-env `app.env` itself — generated at provisioning time and
  lives only on the server. Source of truth for the integration env
  vars (Google, Gumroad, SMTP) once you fill them in.
