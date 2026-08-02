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

echo "== DB backups: keeping $KEEP newest, moving older to Trash =="
if cd "$SD/data/backups" 2>/dev/null; then
  i=0
  for f in $(ls -t *.db 2>/dev/null); do
    i=$((i+1))
    if [ "$i" -le "$KEEP" ]; then echo "  keep   $f"; else mv "$f" ~/.Trash/ && echo "  trash  $f"; fi
  done
else
  echo "  (no data/backups directory)"
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
