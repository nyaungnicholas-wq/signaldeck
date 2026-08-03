#!/usr/bin/env bash
# Retire the macOS launchd fleet. SignalDeck runs on Windows now.
#
# Why this exists rather than "just don't turn the Mac on": launchd starts these
# at login, so the next time this Mac boots it would start a SECOND daemon
# against its own database — stale since 2026-07-29 — and fork the prediction
# ledger into two divergent histories that cannot be merged. com.signaldeck.tunnel
# runs ngrok, so the same boot would also publish that stale daemon.
#
# This STOPS and DISABLES. It deletes nothing, moves nothing, and unloads no
# plist files, so undoing it is one launchctl enable away — the REVERSE section
# at the end prints the exact commands.
#
# Not -e: a job that is already stopped is a SUCCESS, not a failure, and one
# missing label must never abort the other thirteen.
set -uo pipefail

SIGNALDECK_LABELS=(
  com.signaldeck.daemon
  com.signaldeck.tunnel
  com.signaldeck.web
  com.signaldeck.accuracy
  com.signaldeck.awake
  com.signaldeck.bias
  com.signaldeck.cleanup
  com.signaldeck.daily-refresh
  com.signaldeck.market-close
  com.signaldeck.market-open
  com.signaldeck.research-liveness
  com.signaldeck.restore
)

# These belong to other projects that shared this Mac's fleet. Same reasoning:
# both write data, and both now live on Windows.
OTHER_LABELS=( com.tickstream.daemon com.stocktrader.hud )

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "ERROR: this script is for the Mac. SignalDeck now runs on Windows." >&2
  exit 1
fi

# Not UID — that name is readonly in bash and assigning it fails under set -u.
UID_NUM="$(id -u)"
DOMAIN="gui/$UID_NUM"
FLEET_RE='signaldeck|tickstream|stocktrader'

show_loaded() {
  local found
  found="$(launchctl list | grep -E "$FLEET_RE" || true)"
  if [[ -z "$found" ]]; then
    echo "  (none loaded)"
  else
    echo "$found" | sed 's/^/  /'
  fi
}

echo "== BEFORE: loaded jobs matching the fleet =="
show_loaded

FAILED=0
stop_label() {
  local label="$1"
  # bootout stops it now; failure just means it was not loaded.
  launchctl bootout "$DOMAIN/$label" 2>/dev/null || true
  # disable is the part that survives a reboot.
  launchctl disable "$DOMAIN/$label" 2>/dev/null || true
  if launchctl print "$DOMAIN/$label" >/dev/null 2>&1; then
    echo "  STILL LOADED: $label"
    # Not ((FAILED++)): post-increment evaluates to the OLD value, so it returns
    # exit status 1 when FAILED is 0 — a booby trap for any future set -e.
    FAILED=$((FAILED + 1))
  else
    echo "  stopped + disabled: $label"
  fi
}

echo
echo "== SignalDeck jobs =="
for label in "${SIGNALDECK_LABELS[@]}"; do
  stop_label "$label"
done

echo
echo "== Other jobs sharing this fleet (tickstream, stock-trader) =="
for label in "${OTHER_LABELS[@]}"; do
  stop_label "$label"
done

echo
echo "== AFTER: loaded jobs matching the fleet =="
show_loaded

# launchd is not the only way these start — one launched by hand from a shell
# outlives every bootout above. Report the actual PIDs so they can be killed by
# hand; killing them from here would be a surprise this script has no mandate for.
echo
echo "== Stray processes =="
STRAY=0
for proc in signaldeckd tickstreamd ngrok; do
  hits="$(pgrep -fl "$proc" || true)"
  if [[ -n "$hits" ]]; then
    echo "  WARNING: $proc still running — kill these by hand:"
    echo "$hits" | sed 's/^/    /'
    STRAY=1
  else
    echo "  $proc: not running"
  fi
done

# Printed BEFORE the exits, or it would never print at all.
echo
echo "== REVERSE (re-enable this Mac's fleet) =="
echo "  for L in ${SIGNALDECK_LABELS[*]}; do"
echo "    launchctl enable \"gui/$UID_NUM/\$L\""
echo "    launchctl bootstrap \"gui/$UID_NUM\" \"\$HOME/Library/LaunchAgents/\$L.plist\""
echo "  done"
echo "  # Do NOT do this while Windows is running the daemon: two writers fork the ledger."

echo
if [[ "$FAILED" -eq 0 && "$STRAY" -eq 0 ]]; then
  echo "OK: macOS fleet retired. SignalDeck runs on Windows."
  exit 0
fi
echo "ATTENTION: $FAILED job(s) still loaded, stray processes: $STRAY. See above."
exit 1
