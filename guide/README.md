# Operator's guide

Everything you need to run **shopifygmc.com** day-to-day. If you only
read one thing, read the next four.

## Contents

| Doc | When you need it |
| --- | --- |
| **[`access.md`](./access.md)** | First time logging in, or you forgot a URL |
| **[`mail.md`](./mail.md)** | Add / change / delete email addresses, change a password |
| **[`ops.md`](./ops.md)** | Deploys, logs, restarts, what to do when something breaks |
| **[`credentials.md`](./credentials.md)** | Where every secret lives + how to rotate it |

## Quick links

- App (prod): <https://shopifygmc.com>
- App (staging): <https://staging.shopifygmc.com>
- Platform admin: <https://shopifygmc.com/admin/login>
- Webmail: <https://mail.shopifygmc.com>
- Mail-management UI: <https://shopifygmc.com/admin/mail>
- Health probes: <https://shopifygmc.com/healthz> · <https://shopifygmc.com/readyz>
- GitHub repo: <https://github.com/CalilDrissi/shopify-gmc>

## What's running

One Ubuntu 24.04 VPS at `62.169.16.57` runs both prod and staging
side by side, plus the mail stack:

| Layer | Software |
| --- | --- |
| Edge / TLS | Caddy (auto Let's Encrypt) |
| App | Three Go binaries per env: `server`, `worker`, `scheduler` |
| Data | PostgreSQL 16 (`gmcauditor_prod`, `gmcauditor_staging`) |
| Outbound mail | Postfix + OpenDKIM (DKIM-signs everything) |
| Inbound mail | Postfix virtual mailboxes → Dovecot LMTP → Maildir |
| Webmail | Roundcube + PHP-FPM |

For the developer-side documentation (how the code is laid out,
test suite, contributing), see the root [`README.md`](../README.md)
and [`deploy/README.md`](../deploy/README.md).
