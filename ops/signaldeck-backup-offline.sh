#!/bin/bash
# Offline SQLite backup — runs AFTER market-close stops the daemon, so the
# 2GB+ VACUUM INTO copy never competes with the live app for the (niced,
# LowPriorityIO) daemon's disk/CPU budget. This replaces the in-daemon
# "nightly" backup as the primary path: with the daemon gated to 06:20-13:10
# PT the worker's restart-gate used to fire the full backup at BOOT every
# morning, wedging the dashboard for 10+ minutes (root-caused 2026-07-24).
# The in-daemon worker stays as a failsafe (MinGap raised to 30h): if this
# script keeps succeeding daily, the worker always sees a fresh backup_last_ts
# and skips.
set -u
SD="/Users/natalienyaung/claude code/signaldeck"
DB="$SD/data/signaldeck.db"
DIR="$SD/data/backups"
OFFSITE="$HOME/Library/Mobile Documents/com~apple~CloudDocs/SignalDeckBackups"
KEEP=7
LOG="$SD/logs/backup-offline.log"

log() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" >> "$LOG"; }

[ -f "$DB" ] || { log "SKIP: no db at $DB"; exit 0; }

# Refuse to run against a live daemon — that's the exact contention this
# script exists to avoid. The in-daemon failsafe covers that case.
if pgrep -x signaldeckd >/dev/null 2>&1; then
  log "SKIP: signaldeckd is running (in-daemon worker owns backups while live)"
  exit 0
fi

mkdir -p "$DIR"
TS="$(date +%Y%m%d-%H%M%S)"
TARGET="$DIR/signaldeck-$TS.db"
rm -f "$TARGET"

# caffeinate: don't let the Mac sleep mid-copy (post-close is exactly when it
# wants to). VACUUM INTO gives a consistent compacted copy even against WAL.
if caffeinate -i sqlite3 "$DB" "VACUUM INTO '$TARGET';" 2>>"$LOG"; then
  SIZE=$(du -m "$TARGET" | cut -f1)
  log "OK: $TARGET (${SIZE} MB)"
else
  rm -f "$TARGET"
  log "FAIL: VACUUM INTO error (partial removed)"
  exit 1
fi

# Record success in meta so the in-daemon failsafe worker's restart gate sees
# it and skips at the next boot. Daemon is down, so writing directly is safe.
NOW=$(date +%s)
sqlite3 "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('backup_last_ts','$NOW'),('backup_last_file','$(basename "$TARGET")');" 2>>"$LOG" || log "WARN: meta update failed"

# Prune: keep newest $KEEP timestamped backups (names sort chronologically;
# macOS head has no negative -n, so compute the excess count explicitly).
prune() {
  local n
  n=$(ls "$1"/signaldeck-*.db 2>/dev/null | wc -l | tr -d ' ')
  [ "$n" -le "$KEEP" ] && return 0
  ls "$1"/signaldeck-*.db | sort | head -n $((n - KEEP)) | while read -r f; do
    rm -f "$f" && log "pruned $f"
  done
}
prune "$DIR"

# Offsite copy (best-effort, atomic tmp+rename).
if mkdir -p "$OFFSITE" 2>/dev/null; then
  if caffeinate -i cp "$TARGET" "$OFFSITE/.tmp-$TS" 2>>"$LOG" && mv "$OFFSITE/.tmp-$TS" "$OFFSITE/$(basename "$TARGET")"; then
    sqlite3 "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('backup_last_offsite','$(date +%s)');" 2>>"$LOG"
    log "offsite OK"
    prune "$OFFSITE"
  else
    rm -f "$OFFSITE/.tmp-$TS"
    log "WARN: offsite copy failed (local backup kept)"
  fi
else
  log "WARN: offsite dir unavailable"
fi
