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
# Repo root, derived from this script's own location rather than hardcoded.
# This was "/Users/natalienyaung/claude code/signaldeck" — same defect already
# fixed in ops/accuracy-registry.sh and left standing here, so on any machine
# but the original Mac this script backed up nothing and logged to nowhere.
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# python3 on Windows/Git Bash is a Microsoft Store alias stub that resolves,
# prints "Python was not found", and exits 0 — so `command -v` is not enough,
# the candidate has to actually run. Same resolution as accuracy-registry.sh.
PY=""
for cand in python3 python py; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import sys' >/dev/null 2>&1; then
    PY="$cand"; break
  fi
done
DB="$SD/data/signaldeck.db"
DIR="$SD/data/backups"
OFFSITE="$HOME/Library/Mobile Documents/com~apple~CloudDocs/SignalDeckBackups"
# Retention sized to fit BUDGET_MB (2026-07-26, after data/backups hit 13GB:
# the compress path shipped 07-25 but market-close.sh — its only trigger —
# doesn't fire on weekends, so it never ran while the in-daemon failsafe kept
# adding raw ~1.9GB snapshots). At a ~2GB VACUUM'd DB and ~65% gzip shrink:
# 1 raw (~2GB) + 5 gz (~3.3GB) ≈ 5.3GB, inside the 6GB cap with headroom.
KEEP_RAW=1      # newest generation kept as an instantly-restorable plain .db
KEEP_ZST=5      # older generations kept compressed (zstd -3, gzip fallback)
KEEP_RAW_MAX=2  # backstop cap on plain .db if compression itself keeps failing
BUDGET_MB=6144  # hard cap on du -sm "$DIR" — breach logs, pages, exits 1
LOG="$SD/logs/backup-offline.log"

log() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" >> "$LOG"; }

# Hard footprint assertion: retention regressions must page, not silently
# refill the disk. market-close.sh ignores our exit code, so the banner here
# (same channel as the H9 silence banner below) is what reaches a human.
assert_budget() {
  local used
  used=$(du -sm "$DIR" 2>/dev/null | cut -f1)
  if [ "${used:-0}" -gt "$BUDGET_MB" ]; then
    log "FAIL: backups footprint ${used}MB exceeds budget ${BUDGET_MB}MB — retention is not holding"
    osascript -e "display notification \"data/backups is ${used} MB (budget ${BUDGET_MB} MB) — backup compression/prune is not holding.\" with title \"SignalDeck: backup footprint over budget\"" >/dev/null 2>&1
    return 1
  fi
  log "budget OK: ${used:-0}MB of ${BUDGET_MB}MB"
  return 0
}

# --prune-only: compress/prune/assert WITHOUT taking a new backup. Never
# touches the live DB, so it is safe (and gate-exempt) while the daemon runs —
# the weekend/failsafe catch-up path the 13GB pile-up proved we need.
PRUNE_ONLY=false
[ "${1:-}" = "--prune-only" ] && PRUNE_ONLY=true

[ -f "$DB" ] || { log "SKIP: no db at $DB"; exit 0; }

# Refuse to run against a live daemon — that's the exact contention this
# script exists to avoid. The in-daemon failsafe covers that case. Still
# assert the budget: failsafe backups pile up raw on exactly this path.
if [ "$PRUNE_ONLY" != true ] && pgrep -x signaldeckd >/dev/null 2>&1; then
  log "SKIP: signaldeckd is running (in-daemon worker owns backups while live)"
  assert_budget || exit 1
  exit 0
fi

mkdir -p "$DIR"
TS="$(date +%Y%m%d-%H%M%S)"
TARGET="$DIR/signaldeck-$TS.db"

if [ "$PRUNE_ONLY" != true ]; then
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

  # A zero exit from VACUUM INTO is NOT evidence the copy has the data in it.
  # The 2026-08-01 backup exited clean, passed PRAGMA quick_check, cleared the
  # 1000-row bars floor with 3.36M rows -- and carried 14 prediction_ledger rows
  # against 261,164, with no anchors at all. Structure was perfect and the
  # accountability record was gone. So verify CONTENT here, before anything
  # downstream is allowed to call this a backup.
  #
  # Quarantine rather than delete: a backup that failed verification is the
  # evidence for why it failed, and it is the only artifact of that run.
  if [ -z "$PY" ]; then
    log "FAIL: no working python3 found — cannot verify $TARGET has its content; refusing to record an unverified backup"
    exit 1
  fi
  if ! "$PY" "$SD/tools/verify_backup.py" "$TARGET" --live "$DB" >>"$LOG" 2>&1; then
    QDIR="$SD/quarantine/backups-$(date +%Y%m%d)"
    mkdir -p "$QDIR"
    mv "$TARGET" "$QDIR/" 2>>"$LOG"
    log "FAIL: $TARGET failed content verification (see above) — quarantined in $QDIR, NOT recorded as a backup"
    exit 1
  fi

  # Record success in meta so the in-daemon failsafe worker's restart gate sees
  # it and skips at the next boot. Daemon is down, so writing directly is safe.
  NOW=$(date +%s)
  sqlite3 "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('backup_last_ts','$NOW'),('backup_last_file','$(basename "$TARGET")');" 2>>"$LOG" || log "WARN: meta update failed"

  # The daemon is down, so this is the one uncontended moment the WAL (256MB at
  # last audit — live readers pin it all session) can actually truncate to zero.
  sqlite3 "$DB" "PRAGMA wal_checkpoint(TRUNCATE);" >/dev/null 2>>"$LOG" || log "WARN: wal_checkpoint(TRUNCATE) failed"
fi

# Generation policy: the newest $KEEP_RAW stay plain .db (instant restore),
# older ones are compressed in place (zstd -3 cuts SQLite backups 60-70%;
# stock gzip when zstd isn't installed — the explicit path probes matter
# because launchd's PATH won't see a brew-installed zstd), and compressed
# generations are pruned beyond $KEEP_ZST. Only timestamped
# signaldeck-YYYYmmdd-HHMMSS names are managed — ad-hoc snapshots like
# signaldeck-premaint-*.db are left alone. restore-rehearsal.sh knows how to
# pick and decompress .zst/.gz backups.
ZSTD_BIN=""
for z in "$(command -v zstd 2>/dev/null || true)" /opt/homebrew/bin/zstd /usr/local/bin/zstd; do
  [ -n "$z" ] && [ -x "$z" ] && ZSTD_BIN="$z" && break
done
EXT="gz"; [ -n "$ZSTD_BIN" ] && EXT="zst"

compress_file() { # removes the source on success
  if [ -n "$ZSTD_BIN" ]; then
    caffeinate -i "$ZSTD_BIN" -3 -q --rm -f "$1" 2>>"$LOG"
  else
    caffeinate -i gzip -f "$1" 2>>"$LOG"
  fi
}

# (names sort chronologically; macOS head has no negative -n, so compute the
# excess count explicitly)
compress_and_prune() {
  local dir="$1" n
  n=$(ls "$dir"/signaldeck-[0-9]*.db 2>/dev/null | wc -l | tr -d ' ')
  if [ "$n" -gt "$KEEP_RAW" ]; then
    ls "$dir"/signaldeck-[0-9]*.db | sort | head -n $((n - KEEP_RAW)) | while read -r f; do
      if compress_file "$f"; then
        log "compressed ${f}.${EXT}"
      else
        rm -f "${f}.${EXT}"
        log "WARN: compress failed on $f (kept plain)"
      fi
    done
  fi
  # single .db.* glob so a mixed .gz/.zst history (gzip era before zstd got
  # installed) is pruned as one chronological sequence
  n=$(ls "$dir"/signaldeck-[0-9]*.db.* 2>/dev/null | wc -l | tr -d ' ')
  if [ "$n" -gt "$KEEP_ZST" ]; then
    ls "$dir"/signaldeck-[0-9]*.db.* 2>/dev/null | sort | head -n $((n - KEEP_ZST)) | while read -r f; do
      rm -f "$f" && log "pruned $f"
    done
  fi
  n=$(ls "$dir"/signaldeck-[0-9]*.db 2>/dev/null | wc -l | tr -d ' ')
  if [ "$n" -gt "$KEEP_RAW_MAX" ]; then
    ls "$dir"/signaldeck-[0-9]*.db | sort | head -n $((n - KEEP_RAW_MAX)) | while read -r f; do
      rm -f "$f" && log "pruned $f (compression backstop)"
    done
  fi
}
compress_and_prune "$DIR"

if [ "$PRUNE_ONLY" = true ]; then
  log "prune-only pass done"
  assert_budget || exit 1
  exit 0
fi

# Offsite copy (best-effort, atomic tmp+rename).
if mkdir -p "$OFFSITE" 2>/dev/null; then
  if caffeinate -i cp "$TARGET" "$OFFSITE/.tmp-$TS" 2>>"$LOG" && mv "$OFFSITE/.tmp-$TS" "$OFFSITE/$(basename "$TARGET")"; then
    sqlite3 "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('backup_last_offsite','$(date +%s)');" 2>>"$LOG"
    log "offsite OK"
    compress_and_prune "$OFFSITE"
  else
    rm -f "$OFFSITE/.tmp-$TS"
    log "WARN: offsite copy failed (local backup kept)"
  fi
else
  log "WARN: offsite dir unavailable"
fi

# ── H9 (hostile review, 2026-07-26): nothing pages a human ─────────────────
# Unrelated to backups — piggybacked here on purpose. This is the one script
# in the fleet guaranteed to run once a day (market-close.sh -> this script),
# so it needs no new launchd job, and this remediation pass's ops/*.sh budget
# was one new FILE (ops/restore-rehearsal.sh, for H8) — a second standalone
# script for this would have needed its own scheduling to ever actually run.
#
# THE PROBLEM: daemon/cmd/signaldeckd/run.go logs "remote notify: no
# transports configured" exactly ONCE per daemon start (54 occurrences seen
# live) — a line in a log nobody tails routinely. Until
# SIGNALDECK_DISCORD_WEBHOOK / SIGNALDECK_TELEGRAM_BOT_TOKEN+CHAT_ID /
# SIGNALDECK_WEBHOOK_URL is set in daemon/.env, EVERY daemon alert degrades to
# a macOS `display notification` banner that reaches nobody with the lid
# closed — and that silence is itself invisible: nothing distinguishes "quiet
# because nothing is wrong" from "quiet because nobody could see it."
#
# This sets NO credential (deliberately out of scope). It only makes the
# absence loud, once a day, in the same channel every other ops script here
# already uses to reach a human.
NOTIFY_ENV="$SD/daemon/.env"
NOTIFY_LOG="$SD/logs/notify-silence.log"
NOTIFY_COOLDOWN_FILE="$SD/data/.notify-silence-banner-ts"
NOTIFY_COOLDOWN_SECS=$((24 * 3600))

notify_log() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" >> "$NOTIFY_LOG"; }

# has_nonempty KEY — true if KEY=<something non-empty> appears in
# daemon/.env. Never echoes the value, only presence/absence — the same
# redaction discipline internal/notify.Redact applies to error strings.
has_nonempty() {
  [ -f "$NOTIFY_ENV" ] && grep -qE "^${1}=.+" "$NOTIFY_ENV"
}

notify_configured=false
has_nonempty SIGNALDECK_DISCORD_WEBHOOK && notify_configured=true
if has_nonempty SIGNALDECK_TELEGRAM_BOT_TOKEN && has_nonempty SIGNALDECK_TELEGRAM_CHAT_ID; then
  notify_configured=true
fi
has_nonempty SIGNALDECK_WEBHOOK_URL && notify_configured=true

if [ "$notify_configured" = true ]; then
  notify_log "remote notify configured — daemon alerts are pageable beyond this Mac"
else
  notify_log "SILENT-FAILURE MODE — no remote transport configured (Discord/Telegram/webhook all unset). Every daemon alert is macOS-only and goes unseen with the lid closed or nobody at the keyboard. Set SIGNALDECK_DISCORD_WEBHOOK, SIGNALDECK_TELEGRAM_BOT_TOKEN+SIGNALDECK_TELEGRAM_CHAT_ID, or SIGNALDECK_WEBHOOK_URL in $NOTIFY_ENV and restart the daemon."
  # Rate-limited: this banner is warning about missed alerts, so it must not
  # itself become one more notification the operator learns to swipe away.
  now=$(date +%s)
  last=0
  [ -f "$NOTIFY_COOLDOWN_FILE" ] && last=$(cat "$NOTIFY_COOLDOWN_FILE" 2>/dev/null || echo 0)
  if [ $((now - last)) -ge $NOTIFY_COOLDOWN_SECS ]; then
    osascript -e 'display notification "No Discord/Telegram/webhook transport configured — daemon alerts are macOS-only and go unseen when this Mac is unattended." with title "SignalDeck: alerts are SILENT beyond this Mac"' >/dev/null 2>&1
    echo "$now" > "$NOTIFY_COOLDOWN_FILE"
  fi
fi

# ── Hard size assertion (2026-07-26): the 13GB pile-up went unnoticed because
# nothing measured the directory. This is the script's exit status: over
# budget means retention regressed — page and fail instead of refilling disk.
assert_budget || exit 1
exit 0
