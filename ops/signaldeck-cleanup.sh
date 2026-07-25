#!/bin/bash
# SignalDeck housekeeping — reclaim disk from old DB backups and oversized logs.
#
#   signaldeck-cleanup.sh [KEEP]
#     KEEP = how many of the newest DB backups to keep (default 2)
#
# Old backups are MOVED TO ~/.Trash (recoverable — empty the Trash yourself to
# reclaim the space permanently), never hard-deleted. Log files over 20 MB are
# truncated in place (safe for append-mode logs).
set -u
SD="/Users/natalienyaung/claude code/signaldeck"
KEEP="${1:-2}"

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

echo "== Logs over 20 MB: truncating =="
found=0
while IFS= read -r lf; do
  found=1; sz=$(du -h "$lf" | cut -f1); : > "$lf"; echo "  truncated $(basename "$lf") (was $sz)"
done < <(find "$SD/logs" -type f -size +20M 2>/dev/null)
[ "$found" -eq 0 ] && echo "  (none over 20 MB)"

echo "== Disk free now: $(df -h / | awk 'NR==2{print $4}') =="
