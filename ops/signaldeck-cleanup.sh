#!/bin/bash
# SignalDeck housekeeping — reclaim disk from old DB backups and oversized logs.
#
#   signaldeck-cleanup.sh [KEEP]
#     KEEP = how many of the newest DB backups to keep (default 2)
#
# Old backups are MOVED TO ~/.Trash (recoverable — empty the Trash yourself to
# reclaim the space permanently), never hard-deleted. Log files over 10 MB are
# rotated (copy-compress-truncate, 3 gzipped generations kept) — launchd's
# StandardOut/ErrorPath redirection never rotates anything on its own, so
# unbounded growth lands here. Scheduled daily by com.signaldeck.cleanup.plist.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
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
mkdir -p "$TRASH" 2>/dev/null || true
echo "== DB backups: keeping $KEEP newest, moving older to $TRASH =="
if [ ! -d "$TRASH" ]; then
  echo "  SKIP: cannot create $TRASH — refusing to hard-delete a backup instead"
elif cd "$SD/data/backups" 2>/dev/null; then
  i=0
  for f in $(ls -t *.db 2>/dev/null); do
    i=$((i+1))
    if [ "$i" -le "$KEEP" ]; then
      echo "  keep   $f"
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

echo "== Disk free now: $(df -h / | awk 'NR==2{print $4}') =="
