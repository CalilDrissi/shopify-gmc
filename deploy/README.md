# Deploy artifacts

Files used to bring `shopifygmc.com` (prod) up on a single Ubuntu 24.04
VPS, fronted by Caddy. Staging was retired on 2026-05-11 — testing now
happens via the dev codespace + the admin manual plan-override on prod.
To bring staging back, all the scripts here template a list of envs;
extend `prod` → `staging prod` in provision.sh, units.sh, backups.sh,
restore the staging vhost block in units.sh's Caddyfile heredoc.

## Files

- **`provision.sh`** — runs once on a fresh VPS as root. Installs
  Postgres, Caddy, and the system packages we need; creates the
  `deploy` user, `/opt/gmcauditor/prod`, the `gmc_prod` Postgres role
  + `gmcauditor_prod` database (with `BYPASSRLS` since RLS policies
  on every multi-tenant table mean the app role needs to bypass them
  to run cross-tenant work in the worker), and a baseline ufw config
  (allow 22/80/443, deny everything else).

  Replace `PROD_DB_PW_PLACEHOLDER` with a real password before
  running. Generate with:
  ```bash
  openssl rand -base64 24 | tr -d '/+=' | cut -c1-24
  ```

- **`units.sh`** — installs 3 systemd units (server / worker /
  scheduler for prod) plus the Caddyfile and starts everything.
  Idempotent on the units side; the Caddyfile heredoc CLOBBERS the
  live file, so diff before re-running on a bootstrapped box.

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

  Prod's `app.env` should set `SMTP_HOST=localhost` `SMTP_PORT=25`
  `SMTP_FROM=noreply@shopifygmc.com`. Sending from a subdomain would
  need a KeyTable+SigningTable for that subdomain — keep mail going
  out as the apex `From:`.

  **Reverse DNS (PTR) for the box's IPv4 → `mail.shopifygmc.com`** has
  to be set on the VPS provider's side, NOT in Cloudflare. Until then
  Gmail flags outbound as suspicious. On Contabo this lives in the
  customer panel under "Reverse DNS" for the VPS.

- **`webmail.sh`** — turns the outbound-only Postfix from `smtp.sh`
  into a full mail host: extends Postfix to accept inbound for
  `@shopifygmc.com` (virtual mailboxes), installs Dovecot for IMAPS +
  LMTP delivery + SASL bridge, installs Roundcube fronted by Caddy at
  `https://mail.shopifygmc.com`, and adds a Postfix submission service
  on port 587 for the webmail to send through.

  After the script runs, mailbox + alias management is wrapped by the
  `mailbox` helper in this directory (installed at
  `/usr/local/bin/mailbox`):

  ```bash
  ssh root@HOST mailbox add support@shopifygmc.com
  # → prints a freshly generated 24-char password (or pass it as arg 2)
  ssh root@HOST mailbox alias hello@shopifygmc.com support@shopifygmc.com
  ssh root@HOST mailbox passwd support@shopifygmc.com
  ssh root@HOST mailbox del   support@shopifygmc.com   # confirms
  ssh root@HOST mailbox list
  ```

  **Webmail** at `https://mail.shopifygmc.com`. Login with the full
  address and the Dovecot password.

  Under the hood the helper appends to `/etc/dovecot/users` (ARGON2ID
  hashes), `/etc/postfix/vmailbox`, and `/etc/postfix/virtual`, runs
  `postmap` on the maps that need it, and creates the Maildir at
  `/var/mail/vmail/<domain>/<local>/` owned `vmail:vmail`. No service
  reload required — both Dovecot and Postfix re-read these files
  per-request.

  DNS additions this script depends on: `mail.shopifygmc.com` A → the
  box, MX `shopifygmc.com` → `mail.shopifygmc.com priority 10`, and
  update SPF to include `mx` (`v=spf1 mx ip4:62.169.16.57 -all`).

  The script disables apache2 (Roundcube's deb pulls it in but it
  conflicts with Caddy on port 80).

## Layout on the box

```
/opt/gmcauditor/
  prod/
    bin/{server,worker,scheduler,seed,migrate}
    env/app.env             # 0640 root:deploy
    static/  templates/  migrations/  styles/
  marketing/dist/           # Astro build output served as the public site
/var/log/gmcauditor/prod/{server,worker,scheduler}.log
/etc/systemd/system/
  gmcauditor-prod-{server,worker,scheduler}.service
  gmcauditor-prod.target
/etc/caddy/Caddyfile        # vhosts: prod + www, mail.shopifygmc.com
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

# Push artifacts:
rsync -az build/      root@$HOST:/opt/gmcauditor/prod/bin/
rsync -az static/     root@$HOST:/opt/gmcauditor/prod/static/
rsync -az templates/  root@$HOST:/opt/gmcauditor/prod/templates/
rsync -az migrations/ root@$HOST:/opt/gmcauditor/prod/migrations/
ssh root@$HOST 'chown -R deploy:deploy /opt/gmcauditor'

# Generate the env file (see provision.sh comments + the README.md
# `Setup` section for the full env-var list), then push:
scp env.prod root@$HOST:/opt/gmcauditor/prod/env/app.env
ssh root@$HOST 'chown root:deploy /opt/gmcauditor/prod/env/app.env && chmod 0640 /opt/gmcauditor/prod/env/app.env'

# Migrate:
ssh root@$HOST 'cd /opt/gmcauditor/prod && sudo -u deploy bash -c "set -a && source env/app.env && set +a && ./bin/migrate up"'

# Install + start services:
scp deploy/units.sh root@$HOST:/root/
ssh root@$HOST 'bash /root/units.sh'

# Verify:
curl https://shopifygmc.com/healthz   # ok
curl https://shopifygmc.com/readyz    # ready
```

## Operations

```bash
# Tail logs
journalctl -u gmcauditor-prod-server -f
journalctl -u gmcauditor-prod-worker -f

# Restart one service
systemctl restart gmcauditor-prod-server

# Restart everything in an env
systemctl restart gmcauditor-prod.target

# Reload Caddy after a Caddyfile edit
systemctl reload caddy
```

## DNS

- Records live in Cloudflare. Two A records (TTL 300, proxy off so
  Caddy can do Let's Encrypt HTTP-01):
  - `shopifygmc.com`     → 62.169.16.57
  - `www.shopifygmc.com` → 62.169.16.57
  - Plus `mail.shopifygmc.com` for the webmail vhost.
- If you flip Cloudflare proxy on, switch Caddy to Cloudflare-issued
  origin certs (or use the DNS-01 challenge with a Zone:DNS:Edit token
  scoped only to this zone).

## What's NOT in this artifact set

- A CI deploy job that invokes the bootstrap above on push. See the
  `CI auto-deploy` task in `TODO.md`.
- The per-env `app.env` itself — generated at provisioning time and
  lives only on the server. Source of truth for the integration env
  vars (Google, Gumroad, SMTP) once you fill them in.
