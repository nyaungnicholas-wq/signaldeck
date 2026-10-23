#!/bin/bash
# daemon-guard.sh - the Linux port of ops/daemon-guard.ps1, run every 5 minutes
# by signaldeck-daemon-guard.timer. systemd's Restart=always already restarts a
# daemon that EXITS; this covers what that cannot see, in the .ps1's order:
#   1. ops/.maintenance - stand down while downtime is deliberate (90 min ceiling,
#      2 min grace for a holder that has not started yet)
#   2. universe fail-safe - signaldeck-refresh.sh prune-only, never fatal
#   3. down (stopped, or failed after its start limit) -> publication posture,
#      then reset-failed + start
#   4. up but not answering /api/health -> three strikes, 20 min uptime floor,
#      then restart
#   5. stale binary - running revision != git HEAD is logged (deploy-linux.sh fixes it)
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib-portable.sh
. "$SD/ops/lib-portable.sh"
# shellcheck source=../lib-deploy.sh
. "$SD/ops/lib-deploy.sh"
UNIT=signaldeck-daemon.service
LOCK="$SD/ops/.maintenance"
STRIKES="$SD/logs/daemon-guard.strikes"
STRIKES_TO_RESTART=3
MIN_UPTIME_MIN=20
say() { printf '%s daemon-guard: %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$*"; }
alert() { bash "$SD/ops/cloud/alert.sh" "SignalDeck daemon guard" "$*" >/dev/null 2>&1 || true; }

# 1. maintenance lock
if [ -e "$LOCK" ]; then
  age=$(( $(date +%s) - $(stat -c %Y "$LOCK" 2>/dev/null || date +%s) ))
  holder=""
  pgrep -f 'market-close|backup-offline|signaldeck-ctl|deploy-linux|web-release-linux' >/dev/null 2>&1 && holder=yes
  if [ "$age" -lt 120 ] || { [ -n "$holder" ] && [ "$age" -lt 5400 ]; }; then
    say "maintenance lock held (${age}s old${holder:+, holder running}): standing down"
    exit 0
  fi
  say "maintenance lock is stale (${age}s old, holder ${holder:-gone}): removing it"
  rm -f "$LOCK"
fi

# 2. universe fail-safe (never fatal)
minage=0
pgrep -f signaldeck-refresh >/dev/null 2>&1 && minage=7200
bash "$SD/ops/signaldeck-refresh.sh" prune-only "$minage" || say "universe fail-safe exited $? (non-fatal)"

# 3. down
if ! systemctl is-active --quiet "$UNIT"; then
  if [ -x "$SD/bin/signaldeckd" ] && ! sd_check_publication_posture "$SD/bin/signaldeckd" "$SD/daemon" "$(sd_tunnel_running)"; then
    alert "daemon is down and NOT restarted: the installed binary reads as unpublished behind a running tunnel"
    exit 1
  fi
  systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true
  if sd_svc_start com.signaldeck.daemon; then
    say "daemon was down: started"
    rm -f "$STRIKES"
    exit 0
  fi
  say "daemon was down and did NOT start"
  alert "daemon is down and systemctl start failed (journalctl -u $UNIT)"
  exit 1
fi

# 4. hang detection: any HTTP status is an answer, only silence is a strike
code="$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' -m 30 http://127.0.0.1:8322/api/health 2>/dev/null)"
if printf '%s' "$code" | grep -qE '^[1-5][0-9][0-9]$'; then
  rm -f "$STRIKES"
else
  since="$(systemctl show -p ActiveEnterTimestampMonotonic --value "$UNIT" 2>/dev/null)"
  now_us="$(awk '{printf "%d", $1 * 1000000}' "${SD_UPTIME_FILE:-/proc/uptime}" 2>/dev/null)"
  uptime_min=0
  if [ -n "$since" ] && [ "$since" -gt 0 ] 2>/dev/null && [ -n "$now_us" ]; then
    uptime_min=$(( (now_us - since) / 60000000 ))
  else
    say "hang check: could not read the daemon's start time; not counting this miss"
  fi
  strikes=$(( $(cat "$STRIKES" 2>/dev/null || echo 0) + 1 ))
  [ "$uptime_min" -lt "$MIN_UPTIME_MIN" ] && strikes=0
  echo "$strikes" > "$STRIKES"
  if [ "$strikes" -lt "$STRIKES_TO_RESTART" ]; then
    say "running but NOT answering /api/health (strike $strikes of $STRIKES_TO_RESTART, up ${uptime_min} min): waiting"
    exit 0
  fi
  say "HUNG: no /api/health answer on $strikes consecutive checks (up ${uptime_min} min): restarting"
  rm -f "$STRIKES"
  sd_svc_restart com.signaldeck.daemon || { alert "daemon hung and the restart failed"; exit 1; }
  alert "daemon hung (no /api/health answer for ~15 min) and was restarted"
  exit 0
fi

# 5. stale binary (log only; the Windows -CheckOnly preflight does not stop anything either)
head_rev="$(git -C "$SD" rev-parse HEAD 2>/dev/null)"
SD_TOKEN="$(grep -m1 '^SIGNALDECK_API_TOKEN=' "$SD/daemon/.env" 2>/dev/null | cut -d= -f2-)"
SD_AUTH=()
[ -n "$SD_TOKEN" ] && SD_AUTH=(-H "Authorization: Bearer $SD_TOKEN")
run_rev="$(curl -s --noproxy '*' -m 10 -H 'X-Signaldeck: 1' ${SD_AUTH[@]+"${SD_AUTH[@]}"} http://127.0.0.1:8322/api/version 2>/dev/null \
  | sed -n 's/.*"revision"[ ]*:[ ]*"\([^"]*\)".*/\1/p')"
if [ -n "$head_rev" ] && [ -n "$run_rev" ] && [ "$head_rev" != "$run_rev" ]; then
  say "STALE BINARY: running $run_rev, checkout HEAD is $head_rev - run ops/cloud/deploy-linux.sh"
fi
exit 0
