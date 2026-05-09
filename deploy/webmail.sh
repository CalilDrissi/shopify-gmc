#!/usr/bin/env bash
# Stand up inbound mail (Postfix virtual mailboxes) + Dovecot + Roundcube
# at https://mail.shopifygmc.com.
#
# Idempotent: re-running is safe.
set -euo pipefail
log() { echo "[webmail] $*"; }

export DEBIAN_FRONTEND=noninteractive

# --- Install ---
log "installing dovecot, roundcube, php-fpm, sasl bridge"
apt-get update -qq
apt-get install -y -qq \
  dovecot-core dovecot-imapd dovecot-lmtpd dovecot-sieve dovecot-managesieved \
  postfix-pcre \
  roundcube roundcube-sqlite3 roundcube-plugins \
  php-fpm php-sqlite3 php-mbstring php-xml php-curl php-intl php-imagick php-zip \
  apache2-utils

# --- Virtual mail user/storage ---
log "creating vmail user + maildir tree"
id vmail &>/dev/null || useradd --system --uid 5000 --user-group --no-create-home --home /var/mail/vmail --shell /usr/sbin/nologin vmail
install -d -o vmail -g vmail -m 0770 /var/mail/vmail/shopifygmc.com

# --- Postfix: enable inbound + virtual mailboxes ---
log "patching /etc/postfix/main.cf for virtual mailboxes"
postconf -e 'inet_interfaces = all'
postconf -e 'mydestination = localhost'
postconf -e 'virtual_mailbox_domains = shopifygmc.com'
postconf -e 'virtual_mailbox_base = /var/mail/vmail'
postconf -e 'virtual_mailbox_maps = hash:/etc/postfix/vmailbox'
postconf -e 'virtual_alias_maps = hash:/etc/postfix/virtual'
postconf -e 'virtual_uid_maps = static:5000'
postconf -e 'virtual_gid_maps = static:5000'
postconf -e 'virtual_transport = lmtp:unix:private/dovecot-lmtp'
postconf -e 'mailbox_size_limit = 0'
postconf -e 'message_size_limit = 26214400'  # 25 MiB

# Ensure the maps exist (empty is fine; we'll add the admin entry below).
[ -f /etc/postfix/vmailbox ] || touch /etc/postfix/vmailbox
[ -f /etc/postfix/virtual ]  || touch /etc/postfix/virtual

# Add admin@shopifygmc.com to vmailbox if not present.
grep -q '^admin@shopifygmc.com ' /etc/postfix/vmailbox || \
  echo 'admin@shopifygmc.com    shopifygmc.com/admin/' >> /etc/postfix/vmailbox
# Catch-all alias to admin (so postmaster@, abuse@, etc. all land somewhere)
grep -q '^postmaster@shopifygmc.com ' /etc/postfix/virtual || cat >> /etc/postfix/virtual <<'EOF'
postmaster@shopifygmc.com   admin@shopifygmc.com
abuse@shopifygmc.com        admin@shopifygmc.com
hostmaster@shopifygmc.com   admin@shopifygmc.com
ops@shopifygmc.com          admin@shopifygmc.com
dmarc@shopifygmc.com        admin@shopifygmc.com
noreply@shopifygmc.com      admin@shopifygmc.com
EOF
postmap /etc/postfix/vmailbox /etc/postfix/virtual

# --- Submission service (port 587) for Roundcube → Postfix → outbound ---
log "enabling submission (587) with SASL via Dovecot"
if ! grep -q '^submission inet' /etc/postfix/master.cf; then
  cat >> /etc/postfix/master.cf <<'EOF'

submission inet n       -       y       -       -       smtpd
  -o syslog_name=postfix/submission
  -o smtpd_tls_security_level=encrypt
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_sasl_type=dovecot
  -o smtpd_sasl_path=private/auth
  -o smtpd_relay_restrictions=permit_sasl_authenticated,reject
  -o smtpd_recipient_restrictions=permit_sasl_authenticated,reject
  -o milter_macro_daemon_name=ORIGINATING
EOF
fi

# --- Dovecot ---
log "configuring dovecot"
cat > /etc/dovecot/local.conf <<'EOF'
# gmcauditor mail — virtual users in /var/mail/vmail, IMAPS + LMTP only.

protocols = imap lmtp

mail_location = maildir:/var/mail/vmail/%d/%n
mail_uid = vmail
mail_gid = vmail
first_valid_uid = 5000

# IMAP namespace
namespace inbox {
  inbox = yes
  separator = /
  mailbox Sent {
    auto = subscribe
    special_use = \Sent
  }
  mailbox Drafts {
    auto = subscribe
    special_use = \Drafts
  }
  mailbox Trash {
    auto = subscribe
    special_use = \Trash
  }
  mailbox Junk {
    auto = subscribe
    special_use = \Junk
  }
  mailbox Archive {
    auto = subscribe
    special_use = \Archive
  }
}

# IMAPS only, no STARTTLS plain IMAP exposed externally.
service imap-login {
  inet_listener imap {
    port = 0
  }
  inet_listener imaps {
    port = 993
    ssl = yes
  }
}

# LMTP unix socket for Postfix to deliver into
service lmtp {
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    user  = postfix
    group = postfix
    mode  = 0600
  }
}

# Auth socket Postfix's submission service uses for SASL.
service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}

# Passwd-file backed users (one line per virtual mailbox).
passdb {
  driver = passwd-file
  args = scheme=ARGON2ID username_format=%u /etc/dovecot/users
}
# passwd-file (not static) so Dovecot reads the per-user 8th-field extras —
# specifically `userdb_quota_rule=*:storage=<size>` for per-mailbox quotas.
# `override_fields` pins uid/gid to the vmail user/group regardless of what
# each line says — historic lines have `gid=5000` literally but no group
# with gid 5000 exists on the box (vmail's gid is OS-assigned, e.g. 986).
# Without override_fields, mail would deliver as gid=5000 → permission bugs.
# `default_fields` for home is a safety net for lines that omit it.
userdb {
  driver = passwd-file
  args = username_format=%u /etc/dovecot/users
  default_fields = home=/var/mail/vmail/%d/%n
  override_fields = uid=vmail gid=vmail
}

# Quota plugin — maildir backend keeps a `maildirsize` file per mailbox so
# reads stay cheap (no `du`). Default = 1G, overridable per-mailbox via the
# `userdb_quota_rule` extra field on the user's line.
mail_plugins = $mail_plugins quota
protocol imap {
  mail_plugins = $mail_plugins quota imap_quota
}
protocol lmtp {
  mail_plugins = $mail_plugins quota sieve
}
plugin {
  quota = maildir:User quota
  quota_rule  = *:storage=1G
  quota_rule2 = Trash:storage=+100M
  quota_status_success    = DUNNO
  quota_status_nouser     = DUNNO
  quota_status_overquota  = "552 5.2.2 Mailbox is full / Quota exceeded"
}

# Postfix queries this socket pre-DATA to bounce over-quota recipients with
# a real 552 DSN instead of accepting then dropping silently.
service quota-status {
  executable = quota-status -p postfix
  unix_listener /var/spool/postfix/private/quota-status {
    user = postfix
  }
}

# TLS: use Caddy's Let's Encrypt cert (path resolved in the wrapper script
# below — Caddy's storage layout is stable but the directory name is per-host).
ssl = yes
ssl_min_protocol = TLSv1.2
EOF

# Touch users file, mode 0640 so dovecot can read.
[ -f /etc/dovecot/users ] || touch /etc/dovecot/users
chown root:dovecot /etc/dovecot/users
chmod 0640 /etc/dovecot/users

# --- Hand off TLS cert location once Caddy has the cert ---
# Caddy stores certs under /var/lib/caddy/.local/share/caddy/certificates/...
# We'll point Dovecot + Postfix at the cert via symlinks the caddy.service
# can refresh on renewal (Caddy's reload re-touches the files; Dovecot is
# resilient to underlying file changes).
write_tls_glue() {
  local host="$1"
  local cert_dir
  # Newer Caddy uses acme-v02.api.letsencrypt.org-directory; older uses
  # acme.api.letsencrypt.org-directory. Resolve at run-time.
  cert_dir=$(find /var/lib/caddy -type d -name "$host" 2>/dev/null | head -1)
  if [ -z "$cert_dir" ]; then
    echo "[webmail]   (no Caddy cert yet for $host — will retry after Caddy reload)"
    return 1
  fi
  postconf -e "smtpd_tls_cert_file = $cert_dir/$host.crt"
  postconf -e "smtpd_tls_key_file  = $cert_dir/$host.key"
  cat > /etc/dovecot/conf.d/99-tls.conf <<EOF2
ssl_cert = <$cert_dir/$host.crt
ssl_key  = <$cert_dir/$host.key
EOF2
  return 0
}

# --- Caddy: add the mail vhost (TLS only) + Roundcube alias ---
log "adding mail.shopifygmc.com vhost to Caddy"
if ! grep -q 'mail.shopifygmc.com' /etc/caddy/Caddyfile; then
  cat >> /etc/caddy/Caddyfile <<'EOF'

mail.shopifygmc.com {
        encode gzip
        root * /var/lib/roundcube/public_html
        php_fastcgi unix//run/php/php-fpm.sock
        file_server
        log {
                output file /var/log/caddy/webmail-access.log {
                        roll_size 50mb
                        roll_keep 7
                }
        }
        # Discourage indexing
        header X-Robots-Tag "noindex, nofollow"
}
EOF
fi
systemctl reload caddy

# Wait for Caddy to grab the cert (tries up to ~60s)
log "waiting for Let's Encrypt cert on mail.shopifygmc.com"
for i in $(seq 1 12); do
  if write_tls_glue mail.shopifygmc.com; then
    log "  cert wired into Postfix + Dovecot"
    break
  fi
  sleep 5
done

# --- Roundcube wiring: point at localhost IMAP + submission ---
log "configuring Roundcube"
cat > /etc/roundcube/config.inc.php <<'EOF'
<?php
$config['db_dsnw'] = 'sqlite:////var/lib/roundcube/roundcube.sqlite?mode=0640';
$config['imap_host'] = 'ssl://localhost:993';
$config['smtp_host'] = 'tls://localhost:587';
$config['smtp_user'] = '%u';
$config['smtp_pass'] = '%p';
$config['support_url'] = '';
$config['product_name'] = 'gmcauditor mail';
$config['des_key'] = 'PLACEHOLDER_DES_KEY_REPLACED_BELOW';
$config['plugins'] = ['archive', 'zipdownload', 'managesieve'];
$config['mail_pagesize'] = 50;
$config['enable_installer'] = false;
$config['use_https'] = true;
$config['login_logo'] = '';
$config['session_lifetime'] = 30;
EOF
# Replace the placeholder DES key with a fresh one (24-byte random base64)
DES_KEY=$(openssl rand -base64 24 | tr -d '/+=')
sed -i "s/PLACEHOLDER_DES_KEY_REPLACED_BELOW/$DES_KEY/" /etc/roundcube/config.inc.php

install -d -o www-data -g www-data /var/lib/roundcube
chown -R www-data:www-data /var/lib/roundcube

# --- Postfix: wire the quota policy service into recipient restrictions ---
# `check_policy_service` MUST come before `permit_mynetworks` — otherwise
# permit_mynetworks short-circuits with OK for any localhost-origin mail
# (Roundcube → submission, scripts on the box, alias forwards) and the quota
# check never runs. With it first, the policy returns DUNNO for in-quota
# recipients (Postfix continues to the next rule) and 552 for over-quota.
log "wiring Postfix quota policy"
# Strip any prior copies of the policy line out of the existing value, then
# prepend it. Use awk for the strip — sed's `/` separator clashes with the
# `unix:private/quota-status` socket path. Rebuilding to a known-good order
# every run is the simplest idempotent path.
desired_first="check_policy_service unix:private/quota-status"
current_rcpt=$(postconf -h smtpd_recipient_restrictions 2>/dev/null || true)
cleaned=$(printf '%s' "$current_rcpt" | awk -v drop="$desired_first" 'BEGIN{RS=","} {
  gsub(/^[ \t]+|[ \t]+$/, "");
  if ($0 == drop || $0 == "") next;
  out = (out=="" ? $0 : out ", " $0);
} END { print out }')
if [ -z "$cleaned" ]; then
  postconf -e "smtpd_recipient_restrictions = $desired_first, permit_mynetworks, reject_unauth_destination"
else
  postconf -e "smtpd_recipient_restrictions = $desired_first, $cleaned"
fi

# --- Reload + baseline existing mailboxes ---
systemctl restart php*-fpm
# `doveconf -n` exits non-zero on parse error — bail before reload to avoid
# leaving Dovecot in a broken state on a typo in local.conf.
if ! doveconf -n >/dev/null 2>&1; then
  log "ERROR: doveconf -n rejected the new config; not reloading dovecot"
  doveconf -n 2>&1 | tail -20
  exit 1
fi
systemctl reload dovecot
systemctl reload postfix
systemctl reload caddy

# Recalc every existing mailbox so the new quota plugin has an accurate
# baseline maildirsize for boxes that pre-date this rollout. Idempotent:
# re-running just rewrites maildirsize from current state.
if [ -f /etc/dovecot/users ]; then
  log "recalculating quota for existing mailboxes"
  awk -F: '{print $1}' /etc/dovecot/users | while read -r u; do
    [ -n "$u" ] || continue
    doveadm quota recalc -u "$u" >/dev/null 2>&1 || true
  done
fi

log "DONE — to add a mailbox: mailbox add user@shopifygmc.com (CLI installs default 1G quota)"
