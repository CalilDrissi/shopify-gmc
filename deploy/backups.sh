#!/usr/bin/env bash
# Provision nightly Postgres backups for staging + prod.
#
# Run as root on the prod box. Idempotent — re-running re-installs
# the helper script and the cron entry without duplicating either.
#
# What it does:
#   1. Writes /usr/local/bin/gmcauditor-backup-db (the worker script).
#   2. Drops a /etc/cron.d/gmcauditor-backups entry: 03:00 UTC nightly.
#   3. Creates /var/backups/gmcauditor/{staging,prod} with mode 0700,
#      owned by root.
#   4. Runs the worker once immediately so we have a first dump on disk.
#
# Worker behaviour:
#   - For each env (staging, prod): reads DATABASE_URL out of
#     /opt/gmcauditor/<env>/env/app.env (the prod/staging-flavoured one,
#     same source the systemd units use) and runs `pg_dump --format=c
#     --no-owner --no-privileges` to a date-stamped .dump file.
#   - Keeps the 7 most-recent dumps per env; older ones get deleted.
#   - On failure (pg_dump non-zero, disk full, malformed env file) sends
#     mail to OPERATOR_EMAIL via /usr/sbin/sendmail.
#   - Logs every run + every error to /var/log/gmcauditor/backups.log.
#
# Restore (informational, not automated here):
#   sudo -u deploy pg_restore --clean --if-exists --no-owner \
#     -d "$DATABASE_URL" /var/backups/gmcauditor/prod/2026-05-11_03-00.dump

set -euo pipefail

BACKUP_ROOT=/var/backups/gmcauditor
LOG_DIR=/var/log/gmcauditor
WORKER=/usr/local/bin/gmcauditor-backup-db
CRON=/etc/cron.d/gmcauditor-backups
RETAIN=7
ENVS=(staging prod)

mkdir -p "$LOG_DIR"
for env in "${ENVS[@]}"; do
  mkdir -p "$BACKUP_ROOT/$env"
done
chmod 0700 "$BACKUP_ROOT"
chown -R root:root "$BACKUP_ROOT"

cat > "$WORKER" <<'WORKER_EOF'
#!/usr/bin/env bash
# Worker script invoked by cron. Dumps each env's DB to
# /var/backups/gmcauditor/<env>/<YYYY-MM-DD_HH-MM>.dump, rotates to
# the 7 most-recent files, and notifies the operator on failure.

set -uo pipefail
BACKUP_ROOT=/var/backups/gmcauditor
LOG_FILE=/var/log/gmcauditor/backups.log
RETAIN=7
ENVS=(staging prod)
NOW=$(date -u +%Y-%m-%d_%H-%M)
OPERATOR_EMAIL=${OPERATOR_EMAIL:-ops@shopifygmc.com}
MAIL_FROM=${MAIL_FROM:-noreply@shopifygmc.com}

log() {
  echo "$(date -uIseconds) $*" >> "$LOG_FILE"
}

notify() {
  local subject="$1"; shift
  local body="$*"
  if command -v sendmail >/dev/null; then
    {
      echo "From: gmcauditor backups <$MAIL_FROM>"
      echo "To: $OPERATOR_EMAIL"
      echo "Subject: $subject"
      echo "Content-Type: text/plain; charset=UTF-8"
      echo
      echo "$body"
      echo
      echo "Last 20 log lines:"
      tail -n 20 "$LOG_FILE"
    } | sendmail -f "$MAIL_FROM" "$OPERATOR_EMAIL" || true
  fi
}

overall_ok=true
for env in "${ENVS[@]}"; do
  ENV_FILE="/opt/gmcauditor/$env/env/app.env"
  if [[ ! -r "$ENV_FILE" ]]; then
    log "[$env] env file $ENV_FILE not readable, skipping"
    overall_ok=false
    notify "gmcauditor backup FAILED ($env)" "Env file $ENV_FILE not readable by root."
    continue
  fi
  DB_URL=$(grep -E '^DATABASE_URL=' "$ENV_FILE" | head -1 | cut -d= -f2-)
  if [[ -z "$DB_URL" ]]; then
    log "[$env] no DATABASE_URL in $ENV_FILE"
    overall_ok=false
    notify "gmcauditor backup FAILED ($env)" "No DATABASE_URL in $ENV_FILE."
    continue
  fi

  OUT="$BACKUP_ROOT/$env/${NOW}.dump"
  log "[$env] dumping → $OUT"
  if pg_dump --format=c --no-owner --no-privileges --dbname="$DB_URL" --file="$OUT" 2>>"$LOG_FILE"; then
    SIZE=$(stat -c%s "$OUT")
    log "[$env] ok ($SIZE bytes)"
    chmod 0600 "$OUT"
  else
    rc=$?
    log "[$env] pg_dump failed rc=$rc"
    rm -f "$OUT"
    overall_ok=false
    notify "gmcauditor backup FAILED ($env)" "pg_dump returned $rc. Check /var/log/gmcauditor/backups.log."
    continue
  fi

  # Rotation: keep newest $RETAIN .dump files, delete older.
  cd "$BACKUP_ROOT/$env"
  ls -t *.dump 2>/dev/null | tail -n +$((RETAIN + 1)) | xargs -r rm -f
done

if $overall_ok; then
  log "all envs backed up successfully"
fi
WORKER_EOF

chmod 0755 "$WORKER"
chown root:root "$WORKER"

# Cron: 03:00 UTC daily. CRON runs as root; PATH explicit because cron's
# default PATH doesn't include pg_dump on some Debian setups.
cat > "$CRON" <<EOF
# Nightly Postgres backups for gmcauditor (staging + prod).
# Managed by deploy/backups.sh — re-run that script to update.
SHELL=/bin/bash
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
0 3 * * * root $WORKER
EOF
chmod 0644 "$CRON"

# Reload cron so our entry takes effect immediately.
systemctl reload cron || systemctl restart cron

echo "Running first backup now..."
"$WORKER"

echo
echo "=== Latest backups ==="
for env in "${ENVS[@]}"; do
  echo "--- $env ---"
  ls -lh "$BACKUP_ROOT/$env" 2>/dev/null || echo "(none yet)"
done

echo
echo "=== Log tail ==="
tail -n 10 "$LOG_DIR/backups.log" 2>/dev/null || echo "(no log yet)"

echo
echo "Done. Cron entry installed at $CRON; runs daily at 03:00 UTC."
echo "Restore a dump with:"
echo "  sudo -u deploy pg_restore --clean --if-exists --no-owner -d \"\$DATABASE_URL\" $BACKUP_ROOT/<env>/<file>.dump"
