#!/bin/bash
# alert.sh TITLE BODY - the headless stand-in for ops/lib-notify.ps1 (whose
# second channel was a desktop balloon nobody sees on a VM). Always appends to
# logs/health-alerts.log and the journal; then posts to whichever webhook
# transports daemon/.env names - the same variables the daemon's own notifier
# reads (internal/notify). Values are read one key at a time and never printed.
# SMTP and Telegram stay with the daemon (/api/notify-status). Never fails the caller.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
title="${1:-SignalDeck alert}"
body="${2:-}"
mkdir -p "$SD/logs" 2>/dev/null
printf '%s ALERT %s - %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$title" "$body" >> "$SD/logs/health-alerts.log" 2>/dev/null
command -v logger >/dev/null 2>&1 && logger -t signaldeck-alert -- "$title - $body" 2>/dev/null

envval() { grep -m1 "^$1=" "$SD/daemon/.env" 2>/dev/null | cut -d= -f2- | tr -d '\r'; }
json() { # KIND -> the payload that transport accepts
  python3 - "$1" "$title" "$body" <<'PY'
import json, sys, time
kind, title, body = sys.argv[1:4]
text = title + ": " + body
if kind == "discord":
    print(json.dumps({"content": text[:1900]}))
elif kind == "slack":
    print(json.dumps({"text": text}))
else:
    print(json.dumps({"title": title, "body": body, "kind": "ops", "ts": int(time.time())}))
PY
}
post() { # URL KIND
  [ -n "$1" ] || return 0
  json "$2" | curl -s -m 10 -o /dev/null -H 'Content-Type: application/json' --data-binary @- "$1" 2>/dev/null \
    || printf '%s ALERT delivery via %s failed\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$2" >> "$SD/logs/health-alerts.log" 2>/dev/null
}
post "$(envval SIGNALDECK_DISCORD_WEBHOOK)" discord
post "$(envval SIGNALDECK_SLACK_WEBHOOK)" slack
post "$(envval SIGNALDECK_WEBHOOK_URL)" webhook
exit 0
