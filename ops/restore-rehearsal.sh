#!/bin/bash
# Restore rehearsal — H8 (hostile review, 2026-07-26): prove a backup can
# actually be RESTORED, not just that VACUUM INTO or a file copy returned no
# error. Before this script existed, nothing in the fleet ever restored a
# backup anywhere — "success" was daemon/internal/backup's
# os.Stat(target)==nil, which a corrupt-but-nonzero-sized file always
# satisfies, and KEEP=7 rotation would then age the last good generation out
# from under a silently-broken one.
#
# What backup.go's own PRAGMA quick_check (added alongside this script) does
# NOT cover: it verifies the copy the moment it's written, on the SAME disk,
# in the SAME process. It cannot catch bit rot after the fact, an offsite
# sync that corrupts bytes days later, or a restore procedure that nobody
# has actually rehearsed and turns out to be wrong when it's needed for
# real. This script is the end-to-end drill: take the newest backup — same
# one an operator would reach for during an actual incident — restore it to
# an ISOLATED temp path (never touches anything live), open it, run
# PRAGMA quick_check, and check a sane row count on `bars` (the core price
# table; an openable-but-empty copy is not a usable backup).
#
# Exit 0 = the newest backup is provably restorable right now.
# Exit 1 = it is not — treat this as a page, not a log line to scroll past.
#
# WIRING: this script does not self-schedule. It is a standalone DR drill —
# run it by hand, or wire it into cron/launchd on a cadence appropriate for
# your risk tolerance (weekly is reasonable: a full quick_check + row count
# over a 2GB+ file is not free, and this is meant to catch slow rot, not
# every single night's backup individually — that's backup.go's job).

set -uo pipefail

SD="/Users/natalienyaung/claude code/signaldeck"
LOCAL_DIR="$SD/data/backups"
OFFSITE_DIR="$HOME/Library/Mobile Documents/com~apple~CloudDocs/SignalDeckBackups"
LOG="$SD/logs/restore-rehearsal.log"

# A real production backup has millions of `bars` rows. Four digits is
# comfortably below any normal size and comfortably above "empty or
# truncated copy" — it separates the failure mode this script exists to
# catch from ordinary day-to-day size variance, without hard-coding a
# brittle exact count.
MIN_BARS_ROWS=1000

log() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" | tee -a "$LOG"; }

pick_newest() {
  # -t sorts newest-first; names are also timestamp-sortable, but mtime is
  # the honest "what would an operator actually grab right now" signal.
  ls -t "$1"/signaldeck-*.db 2>/dev/null | head -n1
}

SRC="$(pick_newest "$LOCAL_DIR")"
SRC_LABEL="local"
if [ -z "$SRC" ]; then
  SRC="$(pick_newest "$OFFSITE_DIR")"
  SRC_LABEL="offsite"
fi
if [ -z "$SRC" ]; then
  log "FAIL: no backup found in '$LOCAL_DIR' or '$OFFSITE_DIR' — nothing to rehearse a restore from"
  exit 1
fi

if ! command -v sqlite3 >/dev/null 2>&1; then
  log "FAIL: sqlite3 CLI not found — cannot verify the restored copy"
  exit 1
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/signaldeck-restore-XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
TARGET="$TMP/$(basename "$SRC")"

log "restore rehearsal: restoring $SRC_LABEL backup $(basename "$SRC") to isolated temp path $TARGET"
if ! cp "$SRC" "$TARGET"; then
  log "FAIL: restore copy from $SRC to $TARGET failed"
  exit 1
fi

RESULT="$(sqlite3 "$TARGET" "PRAGMA quick_check;" 2>&1)"
if [ "$RESULT" != "ok" ]; then
  log "FAIL: PRAGMA quick_check on the restored copy did not report ok — $RESULT"
  exit 1
fi

ROWS="$(sqlite3 "$TARGET" "SELECT COUNT(*) FROM bars;" 2>&1)"
if ! [[ "$ROWS" =~ ^[0-9]+$ ]]; then
  log "FAIL: could not read a bars row count from the restored copy — $ROWS"
  exit 1
fi
if [ "$ROWS" -lt "$MIN_BARS_ROWS" ]; then
  log "FAIL: restored copy has only $ROWS bars rows (floor is $MIN_BARS_ROWS) — treat as an empty/truncated backup, not a usable one"
  exit 1
fi

log "OK: $SRC_LABEL backup $(basename "$SRC") restores clean — quick_check ok, $ROWS bars rows"
exit 0
