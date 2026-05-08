#!/usr/bin/env bash
# Enable Roundcube's `password` plugin so a logged-in mailbox owner can
# change their own password from Settings → Password. We use the `cmd`
# driver — Roundcube pipes the new password on stdin; our wrapper calls
# `mailbox passwd <user> <newpw>` via a tightly-scoped sudo rule.
set -euo pipefail

apt-get install -y -qq roundcube-plugins

# Wrapper script invoked by Roundcube as www-data.
cat > /usr/local/bin/roundcube-passwd-helper <<'EOF'
#!/bin/bash
# Receives new password on stdin; user passed as $1 (Roundcube fills %u).
set -e
USER="$1"
read -r NEWPW
exec sudo -n /usr/local/bin/mailbox passwd "$USER" "$NEWPW"
EOF
chmod 0755 /usr/local/bin/roundcube-passwd-helper

# Sudoers — www-data can ONLY run `mailbox passwd …`, nothing else.
cat > /etc/sudoers.d/mailbox-www <<'EOF'
www-data ALL=(root) NOPASSWD: /usr/local/bin/mailbox passwd *
Defaults:www-data !requiretty
EOF
chmod 0440 /etc/sudoers.d/mailbox-www
visudo -cf /etc/sudoers.d/mailbox-www

# Patch Roundcube config to enable the plugin.
CFG=/etc/roundcube/config.inc.php
if ! grep -q "'password'" $CFG; then
  sed -i "s/\$config\['plugins'\] = \[/\$config\['plugins'\] = ['password', /" $CFG
fi

if ! grep -q "password_driver" $CFG; then
  cat >> $CFG <<'PHPEOF'

// password plugin — drives /usr/local/bin/mailbox via a sudo wrapper
$config['password_driver']      = 'cmd';
$config['password_cmd']         = '/usr/local/bin/roundcube-passwd-helper %u';
$config['password_minimum_length'] = 12;
$config['password_require_nonalpha'] = false;
$config['password_force_save']  = false;
$config['password_log']         = false; // never log the password
PHPEOF
fi

install -d /etc/roundcube/plugins/password
cat > /etc/roundcube/plugins/password/config.inc.php <<'PHPEOF'
<?php
$config['password_driver'] = 'cmd';
$config['password_cmd']    = '/usr/local/bin/roundcube-passwd-helper %u';
$config['password_minimum_length'] = 12;
$config['password_log'] = false;
PHPEOF

systemctl restart php*-fpm
echo "[roundcube-pw] done — Settings → Password is now in Roundcube"
