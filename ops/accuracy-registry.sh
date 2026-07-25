#!/bin/bash
# Continuous validation — re-grade every predictor's claim against its live record.
#
# Runs daily and read-only. The point is not the report, it is the alert: a predictor
# whose live accuracy falls below its claim should announce itself rather than wait to
# be noticed. Silence here means every shipping number is still supported.

set -uo pipefail

SD="/Users/natalienyaung/claude code/signaldeck"
LOG="$SD/logs/accuracy-registry.log"
OUT="$SD/data/accuracy_registry.json"

{
  echo "──────── $(date '+%Y-%m-%dT%H:%M:%S') ────────"
  python3 "$SD/tools/accuracy_registry.py" --json "$OUT"
} >> "$LOG" 2>&1

# Alert only on a predictor that is actively contradicted by its own live record.
# PENDING is normal and must stay quiet, or the alarm stops meaning anything.
if grep -q "^ACTION REQUIRED" "$LOG" 2>/dev/null && \
   tail -60 "$LOG" | grep -q "^ACTION REQUIRED"; then
  n=$(python3 -c "
import json
try:
    d=json.load(open('$OUT'))
    print(sum(1 for r in d['rows'] if r['verdict'].startswith('FAILED')))
except Exception:
    print(0)
")
  if [ "${n:-0}" -gt 0 ]; then
    osascript -e "display notification \"$n predictor(s) contradicted by their own live record — see the accuracy registry.\" with title \"SignalDeck accuracy\"" >/dev/null 2>&1
  fi
fi
