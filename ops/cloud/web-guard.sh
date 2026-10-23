#!/bin/bash
# web-guard.sh - the Linux port of ops/web-guard.ps1 for the one public web
# instance (127.0.0.1:8323), run every 5 minutes by signaldeck-web-guard.timer.
# Restart=always covers a web that exits; this covers one that is up but wrong:
#   1. ops/.maintenance younger than 30 min -> stand down (a release is swapping)
#   2. /login does not answer 200-399 -> restart
#   3. ops/cloud/web-assets-check.sh exit 1 (markup names assets the build
#      cannot serve) -> restart once per BUILD_ID; if a restart did not fix that
#      build, suppress further restarts for it (logs/web-guard-linux.state).
#      Exit 2 (could not measure) restarts nothing.
# Not ported: the quick-tunnel and :3000 halves (README, "Scheduled tasks").
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib-portable.sh
. "$SD/ops/lib-portable.sh"
PORT=8323
LOCK="$SD/ops/.maintenance"
STATE="$SD/logs/web-guard-linux.state"
CHECK="$SD/ops/cloud/web-assets-check.sh"
say() { printf '%s web-guard: %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$*"; }
alert() { bash "$SD/ops/cloud/alert.sh" "SignalDeck web guard" "$*" >/dev/null 2>&1 || true; }
live() {
  local code
  code="$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' -m 15 "http://127.0.0.1:$PORT/login" 2>/dev/null)"
  [ "${code:-0}" -ge 200 ] 2>/dev/null && [ "$code" -lt 400 ]
}
restart_and_wait() { # -> 0 when the restarted web is live AND complete within 45 s
  sd_svc_restart com.signaldeck.web || return 1
  for _ in $(seq 1 15); do
    sleep 3
    live && bash "$CHECK" -p "$PORT" --quiet >/dev/null 2>&1 && return 0
  done
  return 1
}

if [ -e "$LOCK" ]; then
  age=$(( $(date +%s) - $(stat -c %Y "$LOCK" 2>/dev/null || date +%s) ))
  if [ "$age" -lt 1800 ]; then say "maintenance lock held (${age}s old): standing down"; exit 0; fi
  say "maintenance lock is ${age}s old (over the 30 min ceiling): ignoring it"
fi

if ! live; then
  say "$PORT not answering /login: restarting signaldeck-web.service"
  restart_and_wait && { say "$PORT restarted and complete"; exit 0; }
  alert "web on $PORT did not come back after a restart (journalctl -u signaldeck-web)"
  exit 1
fi

bash "$CHECK" -p "$PORT" --quiet
rc=$?
[ "$rc" -eq 0 ] && { rm -f "$STATE"; exit 0; }
if [ "$rc" -ne 1 ]; then say "asset check could not run (exit $rc): UNVERIFIED, restarting nothing"; exit 0; fi

build="$(head -1 "$SD/web/.next/BUILD_ID" 2>/dev/null | tr -d '\r\n ')"
if [ -n "$build" ] && [ "$(cat "$STATE" 2>/dev/null)" = "$build" ]; then
  say "build $build is still incomplete; a restart already failed to fix it - suppressed (release a new build)"
  exit 1
fi
say "$PORT serves markup naming assets it cannot produce: restarting"
if restart_and_wait; then say "restart fixed it"; rm -f "$STATE"; exit 0; fi
[ -n "$build" ] && echo "$build" > "$STATE"
alert "web build ${build:-unknown} is incomplete even after a restart - release a new build (ops/cloud/web-release-linux.sh)"
exit 1
