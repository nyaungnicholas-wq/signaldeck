#!/bin/bash
# SignalDeck housekeeping — reclaim disk from old DB backups and oversized logs.
#
#   signaldeck-cleanup.sh [KEEP]
#     KEEP = how many of the newest DB backups to keep (default 2)
#   signaldeck-cleanup.sh --retention-only
#     run ONLY the dated retention block below (safe by hand: report-only
#     unless SIGNALDECK_RETENTION_ENFORCE=1). SIGNALDECK_SD overrides the root.
#
# Old backups are MOVED TO ~/.Trash (recoverable — empty the Trash yourself to
# reclaim the space permanently), never hard-deleted. Log files over 10 MB are
# rotated (copy-compress-truncate, 3 gzipped generations kept) — launchd's
# StandardOut/ErrorPath redirection never rotates anything on its own, so
# unbounded growth lands here. Scheduled daily by com.signaldeck.cleanup.plist.
set -uo pipefail
SD="${SIGNALDECK_SD:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
RETENTION_ONLY=0
if [ "${1:-}" = "--retention-only" ]; then RETENTION_ONLY=1; shift; fi
KEEP="${1:-2}"
ROTATE_MB=10   # rotate any logs/*.log bigger than this
ROTATE_KEEP=3  # keep this many gzipped generations (.log.1.gz … .log.3.gz)

# The destination was a bare ~/.Trash, which exists on macOS and NOWHERE on
# Windows. `mv` failed, `&&` swallowed the failure, and the loop printed nothing
# — so from the move to Windows this daily task reported a clean run while
# retiring no backup at all, which is the same silent-no-op shape as the alert
# path and the skipped storage report. The directory is created first, and a
# failed move is now reported rather than hidden by the `&&`.
TRASH="${SIGNALDECK_TRASH:-$HOME/.Trash}"

# RETENTION (2026-10-22): dated clean-up of logs/ and the retired-backup holding
# folder ($TRASH), 30 days each by default (SIGNALDECK_LOG_RETENTION_D,
# SIGNALDECK_HOLDING_RETENTION_D). REPORT-ONLY -- lists, deletes nothing --
# unless SIGNALDECK_RETENTION_ENFORCE=1 is in the environment or daemon/.env,
# which is the owner's switch. Never touches data/ (live DB, WAL/SHM,
# data/backups), state files in logs/ (web-release.json,
# .restore-rehearsal-cursor, *.pid, *.json are outside the name allowlist),
# subdirectories, or anything modified inside the window. Holding-folder age is
# file mtime: mv keeps it, so a retired backup ages from when it was written.
ret_enforce() {
  [ "${SIGNALDECK_RETENTION_ENFORCE:-}" = "1" ] && return 0
  # One key read with grep; the file is never sourced (its values are secrets).
  [ "$(grep -m1 '^SIGNALDECK_RETENTION_ENFORCE=' "$SD/daemon/.env" 2>/dev/null | cut -d= -f2- | tr -d '\r')" = "1" ]
}
# ret_sweep LABEL DIR DAYS GLOB...: regular files at maxdepth 1 of DIR older than
# DAYS whose name matches a GLOB; one line each, removed only when enforcing.
ret_sweep() {
  local label="$1" dir="$2" days="$3" f name pat hit st sz age; shift 3
  RET_N=0
  case "$days" in ''|*[!0-9]*|0) echo "RETENTION SKIP $label: '$days' is not a whole number of days > 0"; return 0 ;; esac
  [ -d "$dir" ] || return 0
  while IFS= read -r -d '' f; do
    name="${f##*/}"; hit=0
    for pat in "$@"; do case "$name" in $pat) hit=1; break ;; esac; done
    [ "$hit" = 1 ] || continue
    st=$(stat -c '%s %Y' "$f" 2>/dev/null || stat -f '%z %m' "$f" 2>/dev/null) || continue
    sz="${st% *}"; age=$(( (RET_NOW - ${st#* }) / 86400 ))
    if [ "$RET_MODE" = enforce ]; then
      rm -f -- "$f" || { echo "RETENTION FAILED to remove $f"; continue; }
      echo "RETENTION removed $f ${age}d $sz"
    else
      echo "RETENTION would-remove $f ${age}d $sz"
    fi
    RET_N=$((RET_N+1)); RET_BYTES=$((RET_BYTES+sz))
  done < <(find "$dir" -mindepth 1 -maxdepth 1 -type f -mmin +$((days*1440)) -print0 2>/dev/null)
}
# ret_hold_refusal DIR: prints why DIR must never be swept, or nothing.
ret_hold_refusal() {
  local real home sd data
  [ -n "$1" ] || { echo "empty path"; return; }
  [ -d "$1" ] || return 0
  real=$(cd "$1" && pwd -P) || { echo "cannot resolve"; return; }
  home=$(cd "$HOME" 2>/dev/null && pwd -P); sd=$(cd "$SD" && pwd -P)
  data=$(cd "$SD/data" 2>/dev/null && pwd -P || echo "$sd/data")
  case "$real" in
    /) echo "it is /" ;;
    "$home") echo "it is \$HOME" ;;
    "$data"|"$data"/*) echo "it is inside $SD/data" ;;
    *) case "$sd/" in "$real"/*) echo "it is the repo root or one of its parents" ;; esac ;;
  esac
}
ret_run() {
  local logs_n hold_n=0 why
  RET_NOW=$(date +%s); RET_BYTES=0; RET_MODE=report
  ret_enforce && RET_MODE=enforce
  echo "== Retention ($RET_MODE): logs/ over ${SIGNALDECK_LOG_RETENTION_D:-30}d, $TRASH over ${SIGNALDECK_HOLDING_RETENTION_D:-30}d =="
  ret_sweep logs "$SD/logs" "${SIGNALDECK_LOG_RETENTION_D:-30}" '*.log' '*.log.[0-9]' '*.log.[0-9][0-9]' \
    '*.log.[0-9].gz' '*.log.[0-9][0-9].gz' '*.err' '*.out' '*.md' '*.jsonl'
  logs_n=$RET_N
  why=$(ret_hold_refusal "$TRASH")
  if [ -n "$why" ]; then
    echo "RETENTION SKIP holding: refusing $TRASH ($why)"
  else
    ret_sweep holding "$TRASH" "${SIGNALDECK_HOLDING_RETENTION_D:-30}" '*'
    hold_n=$RET_N
  fi
  echo "RETENTION mode=$RET_MODE logs=$logs_n holding=$hold_n bytes=$RET_BYTES"
}
if [ "$RETENTION_ONLY" = 1 ]; then ret_run; exit 0; fi

mkdir -p "$TRASH" 2>/dev/null || true
echo "== DB backups: keeping $KEEP newest, moving older to $TRASH =="
if [ ! -d "$TRASH" ]; then
  echo "  SKIP: cannot create $TRASH — refusing to hard-delete a backup instead"
elif cd "$SD/data/backups" 2>/dev/null; then
  # 2026-10-04 14:05 this loop kept a 0-byte signaldeck-20261003-200108.db (a
  # db-backup run cut off by a shutdown) as one of the "2 newest" and moved the
  # newest sha256-recorded, offsite-verified backup to the Trash for it. Two
  # rules close that: an empty .db is never a generation (retired, not
  # counted), and the newest .db that carries a .sha256 sidecar is never
  # retired, however many unverified copies are newer.
  verified=""
  for f in $(ls -t *.db 2>/dev/null); do
    if [ -s "$f" ] && { [ -f "$f.sha256" ] || [ -f "${f%.db}.sha256" ]; }; then
      verified="$f"
      break
    fi
  done
  i=0
  for f in $(ls -t *.db 2>/dev/null); do
    if [ ! -s "$f" ]; then
      # Under an hour old it may be a backup that is still being written.
      if [ -z "$(/usr/bin/find "$f" -mmin +60 2>/dev/null)" ]; then
        echo "  skip   $f (0 bytes, under an hour old: may still be being written; not counted)"
        continue
      fi
      if mv "$f" "$TRASH/"; then
        echo "  trash  $f (0 bytes: an interrupted backup, not counted)"
      else
        echo "  FAILED to move empty $f to $TRASH — left in place"
      fi
      continue
    fi
    i=$((i+1))
    if [ "$i" -le "$KEEP" ]; then
      echo "  keep   $f"
    elif [ "$f" = "$verified" ]; then
      echo "  keep   $f (newest with a sha256 record)"
    elif mv "$f" "$TRASH/"; then
      echo "  trash  $f"
      # The .sha256 sidecar goes with its artifact. Retiring the .db alone left
      # data/backups holding a 96-byte checksum for a file that is no longer
      # there — a chain of custody pointing at nothing, which reads as evidence
      # until someone tries to verify it. Observed live: a stray
      # signaldeck-20260807-131013.db.sha256 with no matching .db or .db.gz.
      for side in "$f.sha256" "${f%.db}.sha256"; do
        if [ -f "$side" ]; then
          mv "$side" "$TRASH/" || echo "  FAILED to move sidecar $side to $TRASH — left in place"
        fi
      done
    else
      echo "  FAILED to move $f to $TRASH — left in place"
    fi
  done
else
  echo "  (no data/backups directory)"
fi

# HOW MUCH IS IN THERE, AND IS IT EVEN OFF THIS DISK.
# The header promises "reclaim disk" and tells the operator to empty the Trash
# themselves. That instruction is macOS. Under Git Bash $HOME/.Trash is an
# ordinary hidden folder with no Finder behind it and nothing that ever empties
# it, so on Windows this task MOVES bytes and reclaims nothing at all. Measured
# 2026-08-12: 9.9 GB sitting there, including two retired ~5 GB databases.
#
# It was unreclaimed AND invisible: assert_budget in signaldeck-backup-offline.sh
# measures data/backups ONLY, so this pile sits outside the single disk tripwire
# the project has. Nothing deletes anything here -- retiring a backup is not the
# same as destroying it -- but the size and the volume are now stated on every
# run, so "trashed" can no longer read as "reclaimed".
if [ -d "$TRASH" ]; then
  trash_mb=$(du -sm "$TRASH" 2>/dev/null | cut -f1)
  trash_vol=$(df -P "$TRASH" 2>/dev/null | awk 'NR==2{print $6}')
  data_vol=$(df -P "$SD/data" 2>/dev/null | awk 'NR==2{print $6}')
  echo "== Retired-backup holding area: ${trash_mb:-unknown} MB in $TRASH =="
  if [ -n "$trash_vol" ] && [ "$trash_vol" = "$data_vol" ]; then
    echo "  NOT RECLAIMED: $TRASH is on the SAME volume ($trash_vol) as the data"
    echo "  directory, so none of the moves above freed a byte. Delete its"
    echo "  contents to reclaim, or point SIGNALDECK_TRASH at another volume."
  fi
fi

echo "== Logs over ${ROTATE_MB} MB: rotating (keep $ROTATE_KEEP gzipped) =="
found=0
while IFS= read -r lf; do
  found=1
  sz=$(du -h "$lf" | cut -f1)
  # Shift older generations up (.2.gz -> .3.gz, .1.gz -> .2.gz; oldest falls off).
  i=$ROTATE_KEEP
  while [ "$i" -ge 2 ]; do
    prev=$((i-1))
    [ -f "$lf.$prev.gz" ] && mv -f "$lf.$prev.gz" "$lf.$i.gz"
    i=$prev
  done
  # Legacy plain generations from the pre-gzip scheme join the ladder compressed.
  for old in "$lf".[0-9]; do
    [ -f "$old" ] && gzip -f "$old"
  done
  # Copy-compress-then-truncate, never rename: launchd holds the live file open
  # in append mode, so renaming it would just carry the fd (and the growth) to
  # the rotated name while the fresh file never receives a byte.
  gzip -c "$lf" > "$lf.1.gz" && : > "$lf"
  echo "  rotated $(basename "$lf") (was $sz)"
done < <(find "$SD/logs" -maxdepth 1 -type f -name '*.log' -size +"${ROTATE_MB}"M 2>/dev/null)
[ "$found" -eq 0 ] && echo "  (none over ${ROTATE_MB} MB)"

ret_run

echo "== Disk free now: $(df -h / | awk 'NR==2{print $4}') =="
