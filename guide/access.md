# Access & login

How to log into every part of the system.

## Platform admin

The admin panel is where you manage tenants, run audits across the
fleet, see GMC connections, and manage mail addresses. Same account
works on both environments (separate databases — same email + password
on each).

| Where | URL |
| --- | --- |
| Production | <https://shopifygmc.com/admin/login> |

**Credentials**: don't live in this repo. They're either in your
password manager (recommended) or in this chat transcript right now —
either way, the source of truth is **outside the codebase**. See
[`credentials.md`](./credentials.md) for the inventory.

**TOTP (two-factor)**: required on first login. Open any
authenticator app — 1Password, Bitwarden, Google Authenticator, Authy,
Apple Passwords — and scan the QR code shown on the enrollment page.
Type the 6-digit code, you're in.

If you ever lose the TOTP secret you'll need to clear it from the
database and walk enrollment again:

```bash
ssh root@62.169.16.57 'sudo -u postgres psql -d gmcauditor_prod -c \
  "UPDATE platform_admins SET totp_secret=NULL, totp_enrolled_at=NULL WHERE role=\"super\""'
```


### Sidebar tour once you're in

- **Dashboard** — fleet-wide counts.
- **Tenants** — list, suspend/unsuspend, impersonate (read-only inside the tenant).
- **Audits** — every audit run across every tenant.
- **GMC** — every Google Merchant Center connection by status.
- **Mail** — manage email addresses on this server (see [`mail.md`](./mail.md)).
- **Settings** — platform-wide knobs (AI key, etc.).

## Webmail

<https://mail.shopifygmc.com> — Roundcube. Login with the full email
address (e.g. `admin@shopifygmc.com`) and the Dovecot password.

Real mail clients work too — same server, same credentials:

| Setting | Value |
| --- | --- |
| Incoming server | `mail.shopifygmc.com` |
| Incoming protocol | IMAP over SSL |
| Incoming port | `993` |
| Outgoing server | `mail.shopifygmc.com` |
| Outgoing protocol | SMTP with STARTTLS |
| Outgoing port | `587` |
| Username | full address |
| Password | Dovecot password |

## SSH

```bash
ssh -i ~/.ssh/your-key root@62.169.16.57
```

You should put your own SSH key on the box (`ssh-copy-id` does this)
and disable password auth — see [`credentials.md`](./credentials.md#server-ssh).

## App (regular user)

Anyone — including you — signs up at:

- <https://shopifygmc.com/signup>

Verification email arrives at your inbox. Click the link, you're in.
For password reset, use <https://shopifygmc.com/forgot-password>.
