#!/usr/bin/env bash
# Provision a fresh Ubuntu 24.04 box with everything gmcauditor needs.
# Idempotent: re-runnable. Touches no app data on its own.
set -euo pipefail

log() { echo "[provision] $*"; }

log "apt update + install base packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq \
  ca-certificates curl gnupg lsb-release \
  postgresql postgresql-contrib \
  rsync ufw \
  build-essential pkg-config

# --- Caddy from the official repo ---
if ! command -v caddy >/dev/null; then
  log "installing Caddy"
  curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -qq
  apt-get install -y -qq caddy
fi

# --- App layout ---
log "creating /opt/gmcauditor layout"
id deploy &>/dev/null || useradd --system --create-home --home-dir /home/deploy --shell /bin/bash deploy
for env in staging prod; do
  install -d -o deploy -g deploy /opt/gmcauditor/$env/{bin,env,static,templates,migrations,styles,scripts}
  install -d -o deploy -g deploy /var/log/gmcauditor/$env
done

# --- Postgres roles + DBs ---
log "creating postgres roles + databases"
sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='gmc_staging'" | grep -q 1 \
  || sudo -u postgres psql -c "CREATE ROLE gmc_staging LOGIN PASSWORD 'STAGING_DB_PW_PLACEHOLDER' CREATEDB"
sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='gmc_prod'" | grep -q 1 \
  || sudo -u postgres psql -c "CREATE ROLE gmc_prod LOGIN PASSWORD 'PROD_DB_PW_PLACEHOLDER' CREATEDB"
sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='gmcauditor_staging'" | grep -q 1 \
  || sudo -u postgres psql -c "CREATE DATABASE gmcauditor_staging OWNER gmc_staging"
sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='gmcauditor_prod'" | grep -q 1 \
  || sudo -u postgres psql -c "CREATE DATABASE gmcauditor_prod OWNER gmc_prod"
# Required extension for gen_random_uuid()
for db in gmcauditor_staging gmcauditor_prod; do
  sudo -u postgres psql -d $db -c "CREATE EXTENSION IF NOT EXISTS pgcrypto"
  sudo -u postgres psql -d $db -c "CREATE EXTENSION IF NOT EXISTS citext"
done
# BYPASSRLS for the app role so it can write across tenants from the worker.
# RLS still applies for any other role (none currently).
sudo -u postgres psql -c "ALTER ROLE gmc_staging BYPASSRLS"
sudo -u postgres psql -c "ALTER ROLE gmc_prod BYPASSRLS"

# --- Firewall ---
log "configuring ufw"
ufw --force reset >/dev/null
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw allow OpenSSH >/dev/null
ufw allow 80/tcp  >/dev/null
ufw allow 443/tcp >/dev/null
ufw --force enable >/dev/null

log "done"
