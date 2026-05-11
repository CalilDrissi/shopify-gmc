#!/usr/bin/env bash
# Provision UptimeRobot monitors for shopifygmc via their v2 REST API.
#
# Prerequisites:
#   1. Create a free UptimeRobot account at https://uptimerobot.com/signUp
#   2. My Settings → API Settings → Main API Key → copy the key
#      (looks like "u1234567-abcdef0123456789...")
#   3. My Settings → Alert Contacts → add at least one (your phone
#      number for SMS, or just your email). Note the "Friendly Name".
#   4. Run: UPTIMEROBOT_API_KEY=u123... bash deploy/uptime-monitors.sh
#
# What it creates (idempotent — re-running updates instead of dup'ing):
#   - HTTPS keyword monitor on https://shopifygmc.com/healthz
#       expects body "ok", every 5 minutes
#   - HTTPS keyword monitor on https://shopifygmc.com/readyz
#       expects body "ready", every 5 minutes (catches DB outages)
#
# Free tier: 50 monitors at 5-minute intervals. We use 2.
#
# Alerts: monitors are attached to ALL existing alert contacts in your
# account (which UptimeRobot lists with `getAlertContacts`). If you
# want to scope, edit the alert-contact-id list in attach_all() below.

set -euo pipefail

: "${UPTIMEROBOT_API_KEY:?set UPTIMEROBOT_API_KEY in the environment}"

API="https://api.uptimerobot.com/v2"

# Helper to POST a form to UptimeRobot's API and pretty-print result.
ur() {
  local endpoint="$1"; shift
  curl -sS -X POST "$API/$endpoint" \
    -H "Cache-Control: no-cache" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "api_key=$UPTIMEROBOT_API_KEY&format=json&$*"
}

echo "=== 1) Listing existing alert contacts ==="
contacts_json=$(ur getAlertContacts)
echo "$contacts_json" | head -c 600; echo
contact_ids=$(echo "$contacts_json" \
  | grep -oE '"id":"[0-9]+"' \
  | sed -E 's/"id":"([0-9]+)"/\1/g' \
  | paste -sd_ -)
if [[ -z "$contact_ids" ]]; then
  echo "✗ no alert contacts found. Create one in UptimeRobot UI first."
  exit 1
fi
# Each contact-id needs a "_0_0" suffix (threshold, recurrence).
alert_arg=$(echo "$contact_ids" | sed -E 's/([0-9]+)/\1_0_0/g')
echo "  alert_contacts arg: $alert_arg"

upsert_monitor() {
  local name="$1"
  local url="$2"
  local interval_seconds="$3"   # 300 for 5m, 900 for 15m
  local keyword="$4"            # body string we expect (empty = HTTP-only)

  echo
  echo "=== ensure monitor: $name ($url) ==="

  # Check if a monitor with this URL already exists.
  existing=$(ur getMonitors "search=$url")
  monitor_id=$(echo "$existing" \
    | grep -oE '"id":[0-9]+' \
    | head -1 \
    | sed 's/"id"://')

  # Type 2 = keyword, 1 = http(s). keyword_type 2 = "exists".
  local type=1 ktype="" kvalue=""
  if [[ -n "$keyword" ]]; then
    type=2
    ktype="&keyword_type=2&keyword_value=$keyword"
  fi

  if [[ -n "$monitor_id" ]]; then
    echo "  → updating id=$monitor_id"
    ur editMonitor \
      "id=$monitor_id&friendly_name=$(printf %s "$name" | jq -Rr @uri)&url=$url&interval=$interval_seconds${ktype}&alert_contacts=$alert_arg"
  else
    echo "  → creating new"
    ur newMonitor \
      "type=$type&friendly_name=$(printf %s "$name" | jq -Rr @uri)&url=$url&interval=$interval_seconds${ktype}&alert_contacts=$alert_arg"
  fi
  echo
}

upsert_monitor "shopifygmc prod healthz"  "https://shopifygmc.com/healthz"  300 "ok"
upsert_monitor "shopifygmc prod readyz"   "https://shopifygmc.com/readyz"   300 "ready"

echo
echo "=== 4) Final monitor list ==="
ur getMonitors | grep -oE '"friendly_name":"[^"]+"|"url":"[^"]+"|"status":[0-9]+' \
  | paste - - - \
  | sed 's/"friendly_name":/  /;s/"url":/  /;s/"status":/  status=/;s/"//g'

echo
echo "✓ Done. Alerts fire to: $(echo "$contacts_json" | grep -oE '"value":"[^"]*"' | head -3 | sed 's/"value":"//g;s/"//g' | paste -sd, -)"
echo
echo "Test an alert without taking the site down:"
echo "  curl 'https://api.uptimerobot.com/v2/editMonitor' \\"
echo "    -d 'api_key=\$UPTIMEROBOT_API_KEY&format=json' \\"
echo "    -d 'id=<monitor-id>&url=https://shopifygmc.com/this-doesnt-exist-on-purpose'"
echo "  (wait 5 min, then point it back at /healthz)"
