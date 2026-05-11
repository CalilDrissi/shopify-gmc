#!/usr/bin/env bash
# Install 3 systemd units (server/worker/scheduler for prod) +
# the per-env Caddyfile, then enable + start everything.
#
# To add staging back: extend `for env in prod` below to
# `for env in staging prod`, restore the staging vhost block in the
# Caddyfile heredoc, and add the staging units to the enable line.
#
# WARNING: re-running this overwrites /etc/caddy/Caddyfile with the
# heredoc below. If you've made out-of-band changes to the live
# Caddyfile (path matchers for app routes, additional vhosts), they
# will be lost. Diff against the live file before running on an
# already-bootstrapped box.
set -euo pipefail

write_unit() {
  local env="$1" svc="$2" args="$3" desc="$4"
  # The server unit shells out to `sudo /usr/local/bin/mailbox` (for
  # /admin/mail), and that CLI writes to /etc/dovecot/users +
  # /etc/postfix/virtual + Maildirs. NoNewPrivileges blocks sudo
  # elevation; ProtectSystem=strict blocks the writes. Worker and
  # scheduler don't shell out and stay hardened.
  local hardening_block
  if [ "$svc" = "server" ]; then
    hardening_block="NoNewPrivileges=false
ProtectSystem=false"
  else
    hardening_block="NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/log/gmcauditor/$env"
  fi
  cat > /etc/systemd/system/gmcauditor-$env-$svc.service <<EOF
[Unit]
Description=$desc ($env)
After=network-online.target postgresql.service
Wants=network-online.target
PartOf=gmcauditor-$env.target

[Service]
Type=simple
User=deploy
Group=deploy
WorkingDirectory=/opt/gmcauditor/$env
EnvironmentFile=/opt/gmcauditor/$env/env/app.env
ExecStart=/opt/gmcauditor/$env/bin/$svc $args
Restart=always
RestartSec=5
TimeoutStopSec=35
KillSignal=SIGTERM
StandardOutput=append:/var/log/gmcauditor/$env/$svc.log
StandardError=append:/var/log/gmcauditor/$env/$svc.log

# Hardening (see write_unit for the per-svc rationale).
$hardening_block
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true

[Install]
WantedBy=multi-user.target
EOF
}

write_target() {
  local env="$1"
  cat > /etc/systemd/system/gmcauditor-$env.target <<EOF
[Unit]
Description=gmcauditor ($env) — server + worker + scheduler
Wants=gmcauditor-$env-server.service gmcauditor-$env-worker.service gmcauditor-$env-scheduler.service

[Install]
WantedBy=multi-user.target
EOF
}

for env in prod; do
  write_unit $env server    ""              "gmcauditor HTTP server"
  write_unit $env worker    "-mode=worker"  "gmcauditor worker"
  write_unit $env scheduler "-mode=scheduler" "gmcauditor scheduler"
  write_target $env
done

# Caddyfile: TLS auto-cert via HTTP-01 (proxy=off in DNS), reverse-proxy
# to the per-env upstreams, propagate request id and forwarded headers.
cat > /etc/caddy/Caddyfile <<'EOF'
{
        # Use a real ACME email so Let's Encrypt can warn us about
        # certs about to expire. Placeholder until you set OPS_EMAIL.
        email ops@shopifygmc.com
}

shopifygmc.com, www.shopifygmc.com {
        encode gzip
        reverse_proxy localhost:8080 {
                header_up X-Forwarded-For {remote_host}
                header_up X-Forwarded-Proto {scheme}
                header_up X-Real-IP {remote_host}
        }
        log {
                output file /var/log/caddy/prod-access.log {
                        roll_size 50mb
                        roll_keep 7
                }
                format json
        }
}

EOF

systemctl daemon-reload
systemctl enable --now gmcauditor-prod-server.service gmcauditor-prod-worker.service gmcauditor-prod-scheduler.service
systemctl reload caddy
echo "[units] all up"
