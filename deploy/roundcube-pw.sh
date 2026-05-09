#!/usr/bin/env bash
# Enable Roundcube's `password` plugin so a logged-in mailbox owner can
# change their own password from Settings → Password.
#
# Uses the `dovecot_passwdfile` driver (ships with Ubuntu's roundcube-plugins
# package; the `cmd` driver does NOT). The driver writes /etc/dovecot/users
# directly, hashing with hash-argon2id (Roundcube method name; matches the
# {ARGON2ID} prefix Dovecot's passwd-file format expects).
#
# Permission: PHP-FPM (www-data) needs read+write on /etc/dovecot/users.
# Solved by adding www-data to the dovecot group + chmod g+w.
set -euo pipefail

apt-get install -y -qq roundcube-plugins

CFG=/etc/roundcube/config.inc.php
PCFG=/etc/roundcube/plugins/password/config.inc.php

# Add password plugin to the enabled plugins array (if not already)
if ! grep -q "'password'" "$CFG"; then
  sed -i "s/\$config\['plugins'\] = \[/\$config\['plugins'\] = ['password', /" "$CFG"
fi

# Wipe any prior password_* config (we re-write it cleanly)
sed -i '/password_/d' "$CFG"

cat >> "$CFG" <<'PHPEOF'

// password plugin — dovecot_passwdfile driver writes /etc/dovecot/users
// using hash-argon2id with the {ARGON2ID} prefix Dovecot reads on auth.
$config['password_driver']                  = 'dovecot_passwdfile';
$config['password_dovecot_passwdfile_path'] = '/etc/dovecot/users';
$config['password_algorithm']               = 'hash-argon2id';
$config['password_algorithm_prefix']        = true;
$config['password_minimum_length']          = 12;
$config['password_log']                     = false;
PHPEOF

install -d /etc/roundcube/plugins/password
cat > "$PCFG" <<'PHPEOF'
<?php
$config['password_driver']                  = 'dovecot_passwdfile';
$config['password_dovecot_passwdfile_path'] = '/etc/dovecot/users';
$config['password_algorithm']               = 'hash-argon2id';
$config['password_algorithm_prefix']        = true;
$config['password_minimum_length']          = 12;
$config['password_log']                     = false;
PHPEOF

# Permission: www-data writes the passwd file via dovecot group
usermod -aG dovecot www-data
chmod g+w /etc/dovecot/users

# Drop any older `cmd` driver wrapper + sudoers from previous attempts
rm -f /usr/local/bin/roundcube-passwd-helper /etc/sudoers.d/mailbox-www

systemctl restart php*-fpm
echo "[roundcube-pw] done — Settings → Password works in Roundcube"
echo "             driver:    dovecot_passwdfile"
echo "             algorithm: hash-argon2id (writes {ARGON2ID}\$argon2id\$… prefix)"
