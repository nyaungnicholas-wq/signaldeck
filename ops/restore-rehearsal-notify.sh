#!/bin/bash
# Thin paging wrapper around restore-rehearsal.sh — H8 wiring.
#
# restore-rehearsal.sh's own contract: "Exit 1 = ... treat this as a page,
# not a log line to scroll past." This wrapper is what makes that true: on
# any non-zero exit it pages through the same transports daemon/internal/
# notify reads from daemon/.env (SIGNALDECK_DISCORD_WEBHOOK, or
# SIGNALDECK_TELEGRAM_BOT_TOKEN + SIGNALDECK_TELEGRAM_CHAT_ID), and always
# fires a local macOS notification so a failure is never silent even before
# a remote transport is configured. Scheduled weekly (Sunday, pre-market)
# by ops/com.signaldeck.restore.plist.

set -uo pipefail

SD="/Users/natalienyaung/claude code/signaldeck"
LOG="$SD/logs/restore-rehearsal.log"
ENV_FILE="$SD/daemon/.env"

"$SD/ops/restore-rehearsal.sh"
RC=$?
[ "$RC" -eq 0 ] && exit 0

# Keep the message free of double quotes — it is embedded verbatim in the
# Discord JSON body below.
MSG="SignalDeck RESTORE REHEARSAL FAILED (exit $RC) — the newest backup did not restore clean. See logs/restore-rehearsal.log and rehearse by hand before trusting the backups. This is a page, not a log line."

if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

PAGED=0
if [ -n "${SIGNALDECK_DISCORD_WEBHOOK:-}" ]; then
  curl -fsS -m 15 -X POST -H 'Content-Type: application/json' \
    --data "{\"content\":\"$MSG\"}" \
    "$SIGNALDECK_DISCORD_WEBHOOK" >/dev/null 2>&1 && PAGED=1
fi
if [ -n "${SIGNALDECK_TELEGRAM_BOT_TOKEN:-}" ] && [ -n "${SIGNALDECK_TELEGRAM_CHAT_ID:-}" ]; then
  curl -fsS -m 15 -X POST \
    --data-urlencode "chat_id=$SIGNALDECK_TELEGRAM_CHAT_ID" \
    --data-urlencode "text=$MSG" \
    "https://api.telegram.org/bot$SIGNALDECK_TELEGRAM_BOT_TOKEN/sendMessage" >/dev/null 2>&1 && PAGED=1
fi

# Local fallback — fires regardless, so the machine's operator sees the
# failure even when no remote transport is configured yet.
osascript -e 'display notification "Restore rehearsal FAILED — newest backup may not be restorable. See logs/restore-rehearsal.log." with title "SignalDeck DR"' >/dev/null 2>&1

if [ "$PAGED" -eq 1 ]; then
  echo "$(date '+%Y-%m-%dT%H:%M:%S') PAGE SENT: restore rehearsal failed (exit $RC) — notified via daemon/.env transport" >>"$LOG"
else
  echo "$(date '+%Y-%m-%dT%H:%M:%S') PAGE NOT DELIVERED REMOTELY: restore rehearsal failed (exit $RC) but no notify transport configured/reachable in daemon/.env (SIGNALDECK_DISCORD_WEBHOOK / SIGNALDECK_TELEGRAM_*) — local notification only" >>"$LOG"
fi

exit "$RC"
