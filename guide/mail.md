# Mail — add / change / delete addresses

You have **three places** that can manage mail addresses. Pick whichever
matches what you're doing.

| What you want | Where |
| --- | --- |
| Manage *any* address (admin task) | <https://shopifygmc.com/admin/mail> |
| Change *your own* password (mailbox owner) | Roundcube → Settings → Password |
| Scriptable / batch ops | `mailbox` CLI over SSH |

## Manage *any* address — `/admin/mail`

Sign in to <https://shopifygmc.com/admin/login> and click **Mail** in
the sidebar. The page has four sections:

### Add a new mailbox

1. Fill in the email (e.g. `support@shopifygmc.com`).
2. Leave the password field blank to get a randomly generated 24-char
   password — it's shown once in a green banner. **Copy it then;
   it's not retrievable later.**
3. The mailbox is reachable immediately — log in to webmail right
   away.

### Add an alias (forward one address to another)

1. **From** — the address that receives mail (e.g. `hello@shopifygmc.com`).
2. **To** — where it's delivered. Either a local mailbox
   (`support@shopifygmc.com`) or any external address
   (`you@gmail.com`).

Aliases are great for catch-all roles: `sales@`, `support@`, `info@`
all forwarded to one inbox.

### Change a password

In the **Mailboxes** table, click **Rotate password** on the row.
Confirms with a browser prompt; banner shows the new generated
password — copy it then.

If you want to choose the password yourself instead of getting a
generated one, use the CLI (below) or have the mailbox owner change
it from inside Roundcube.

### Delete an address

In the **Mailboxes** table, click **Delete** on the row. The browser
prompts you to type the full address back to confirm — that's a
safety net so you can't fat-finger a deletion. The Maildir is wiped
along with the row in Dovecot's user file.

To delete an alias, scroll to the **Aliases** table and click
**Remove** on the row.

## Change *your own* password — Roundcube

Mailbox owners (anyone with an `@shopifygmc.com` address) can rotate
their own password without bothering the admin:

1. <https://mail.shopifygmc.com> → log in with your full address +
   current password.
2. Click the **gear icon** (top-right) — opens **Settings**.
3. In the left sidebar pick **Password**.
4. Type the new password twice. Minimum 12 chars.
   (Roundcube uses your current session's IMAP password as the
   "current" — the form only asks for the new one.)
5. Click **Save**. You'll see a green "Successfully saved." banner.
6. The new password is live immediately — your next IMAP login uses it.

Behind the scenes Roundcube uses the `dovecot_passwdfile` driver to
rewrite the same `/etc/dovecot/users` file the `mailbox` CLI manages,
hashed as `hash-argon2id` with the `{ARGON2ID}` prefix Dovecot reads.

## Scriptable / batch ops — `mailbox` CLI

Useful when adding several at once, or from automation. The helper
lives at `/usr/local/bin/mailbox` on the box; invoke over SSH:

```bash
# Create with a generated password
ssh root@62.169.16.57 mailbox add support@shopifygmc.com

# Create with your own password
ssh root@62.169.16.57 mailbox add jane@shopifygmc.com 'pick-a-password'

# Forward one address to another
ssh root@62.169.16.57 mailbox alias billing@shopifygmc.com cal@gmail.com

# Rotate a password (prints the new one)
ssh root@62.169.16.57 mailbox passwd jane@shopifygmc.com

# Or specify the new password
ssh root@62.169.16.57 mailbox passwd jane@shopifygmc.com 'new-pw-here'

# List everything
ssh root@62.169.16.57 mailbox list

# Delete (interactive — type the full email when prompted)
ssh root@62.169.16.57 mailbox del jane@shopifygmc.com

# Remove an alias
ssh root@62.169.16.57 mailbox unalias billing@shopifygmc.com
```

No service restart is needed after any of these — Dovecot and
Postfix re-read their files on every login / delivery.

## Built-in addresses

These aliases are set up by default (all forward to
`admin@shopifygmc.com`):

- `postmaster@`
- `abuse@`
- `hostmaster@`
- `ops@`
- `dmarc@` (DMARC reports go here)
- `noreply@` (the app sends as this; replies land here)

Don't delete these unless you're sure — `postmaster@` is required by
RFC 5321, and `dmarc@` is where Gmail/Outlook send their daily DMARC
aggregate reports.
