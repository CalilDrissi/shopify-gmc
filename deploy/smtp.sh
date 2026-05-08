#!/usr/bin/env bash
# Stand up Postfix (outbound MTA) + OpenDKIM on shopifygmc.com.
# Idempotent.
set -euo pipefail
log() { echo "[smtp] $*"; }

export DEBIAN_FRONTEND=noninteractive

# Pre-seed Postfix to "internet site" with our hostname, so the install
# doesn't open a curses dialog.
debconf-set-selections <<'EOF'
postfix postfix/main_mailer_type select Internet Site
postfix postfix/mailname string mail.shopifygmc.com
EOF

log "installing postfix + opendkim"
apt-get install -y -qq postfix opendkim opendkim-tools mailutils

# --- Postfix main config ---
log "writing /etc/postfix/main.cf"
cat > /etc/postfix/main.cf <<'EOF'
# gmcauditor outbound MTA — talks to localhost only, signs with OpenDKIM.

myhostname              = mail.shopifygmc.com
mydomain                = shopifygmc.com
myorigin                = $mydomain
mydestination           = localhost
inet_interfaces         = loopback-only
inet_protocols          = ipv4

# Accept mail from the app on 127.0.0.1; reject everything else.
mynetworks              = 127.0.0.0/8 [::1]/128
smtpd_relay_restrictions = permit_mynetworks reject_unauth_destination

# TLS for outbound (use opportunistic, fall back if peer doesn't support it)
smtp_tls_security_level = may
smtp_tls_loglevel       = 1
smtp_tls_CAfile         = /etc/ssl/certs/ca-certificates.crt
smtp_tls_session_cache_database = btree:${data_directory}/smtp_scache

# OpenDKIM milter — signs everything we send out.
smtpd_milters           = inet:localhost:8891
non_smtpd_milters       = inet:localhost:8891
milter_default_action   = accept
milter_protocol         = 6

# Bounce noisy retries faster — typical for transactional mail.
maximal_queue_lifetime  = 1d
bounce_queue_lifetime   = 1d

# Logging
maillog_file            = /var/log/mail.log

# Misc
biff                    = no
append_dot_mydomain     = no
readme_directory        = no
compatibility_level     = 3.6
EOF

# --- DKIM key (selector "mail") ---
log "generating DKIM key (selector mail)"
KEYDIR=/etc/opendkim/keys/shopifygmc.com
mkdir -p "$KEYDIR"
if [ ! -f "$KEYDIR/mail.private" ]; then
  cd "$KEYDIR"
  opendkim-genkey -b 2048 -d shopifygmc.com -s mail
  chown opendkim:opendkim mail.private
  chmod 0600 mail.private
fi

# --- OpenDKIM config ---
log "writing /etc/opendkim.conf"
cat > /etc/opendkim.conf <<'EOF'
# OpenDKIM signs outbound mail from shopifygmc.com.
Syslog                  yes
SyslogSuccess           yes
LogWhy                  yes

UMask                   002
Mode                    s
Canonicalization        relaxed/relaxed
PidFile                 /run/opendkim/opendkim.pid
UserID                  opendkim:opendkim

# Talk to Postfix on localhost:8891
Socket                  inet:8891@localhost

# One domain, one selector.
Domain                  shopifygmc.com
Selector                mail
KeyFile                 /etc/opendkim/keys/shopifygmc.com/mail.private

# Sign everything from inside.
InternalHosts           refile:/etc/opendkim/TrustedHosts
ExternalIgnoreList      refile:/etc/opendkim/TrustedHosts
EOF

mkdir -p /etc/opendkim
cat > /etc/opendkim/TrustedHosts <<'EOF'
127.0.0.1
::1
localhost
shopifygmc.com
*.shopifygmc.com
EOF

# Default opendkim runtime socket dir
mkdir -p /run/opendkim
chown opendkim:opendkim /run/opendkim

systemctl enable opendkim postfix
systemctl restart opendkim
systemctl restart postfix

log "done. DKIM public key at $KEYDIR/mail.txt:"
cat "$KEYDIR/mail.txt"
