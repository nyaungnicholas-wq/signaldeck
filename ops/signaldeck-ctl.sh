#!/bin/bash
# SignalDeck control — start/stop the stack on demand or on the market-hours schedule.
#
#   signaldeck-ctl.sh up       full stack (daemon + tunnel + web), opens the dashboard
#   signaldeck-ctl.sh collect  data collection only (daemon + tunnel) — market-open trigger uses this
#   signaldeck-ctl.sh stop     stop everything — market-close trigger uses this
#   signaldeck-ctl.sh status   show what is running
#
# The service plists (daemon/web/tunnel) are loaded-idle at login (RunAtLoad=false),
# so nothing runs until this script kickstarts it — on the schedule or when you ask.
set -u

DOMAIN="gui/$(id -u)"
LA="$HOME/Library/LaunchAgents"
DAEMON="com.signaldeck.daemon"
TUNNEL="com.signaldeck.tunnel"
WEB="com.signaldeck.web"

kick() {
  # ensure loaded, then start (idempotent)
  launchctl bootstrap "$DOMAIN" "$LA/$1.plist" 2>/dev/null
  launchctl kickstart "$DOMAIN/$1" 2>/dev/null
}
stopsvc() { launchctl kill TERM "$DOMAIN/$1" 2>/dev/null; }
running() { launchctl print "$DOMAIN/$1" 2>/dev/null | awk -F'= ' '/[^a-z]pid = /{print $2; exit}'; }

case "${1:-status}" in
  up)
    kick "$DAEMON"; kick "$TUNNEL"; kick "$WEB"
    echo "SignalDeck up — daemon :8322, web :8323, tunnel. Dashboard: http://127.0.0.1:8323"
    # Wait for the daemon to answer, then pre-warm the dashboard cache before
    # opening the browser — the first cold /api/dashboard build after boot can
    # take 20-60s while workers catch up; warming here means the page you see
    # loads from cache in ~40ms. Cap the wait so the browser always opens.
    echo "warming dashboard cache (first build after boot is the slow one)..."
    for _ in $(seq 1 30); do
      curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8322/api/health && break
      sleep 1
    done
    curl -sf -o /dev/null --max-time 120 -H "X-Signaldeck: 1" http://127.0.0.1:8322/api/dashboard || true
    open "http://127.0.0.1:8323" 2>/dev/null || true
    ;;
  collect)
    kick "$DAEMON"; kick "$TUNNEL"
    echo "SignalDeck collecting — daemon + tunnel up (no web UI)"
    ;;
  stop)
    stopsvc "$WEB"; stopsvc "$TUNNEL"; stopsvc "$DAEMON"
    echo "SignalDeck stopped"
    ;;
  refresh)
    # run the daily full-universe refresh sweep now (ignores the once-per-day guard)
    exec /bin/bash "/Users/natalienyaung/claude code/signaldeck/ops/signaldeck-refresh.sh" force
    ;;
  status)
    for s in "$DAEMON" "$TUNNEL" "$WEB"; do
      if launchctl print "$DOMAIN/$s" >/dev/null 2>&1; then
        p="$(running "$s")"
        [ -n "$p" ] && echo "$s: running (pid $p)" || echo "$s: loaded-idle (stopped)"
      else
        echo "$s: not loaded"
      fi
    done
    ;;
  *) echo "usage: $(basename "$0") {up|collect|stop|status|refresh}"; exit 1;;
esac
