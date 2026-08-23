#!/bin/bash
# Scheduled structural revalidation.
#
# STRATEGY_DECK.md §8 recorded the defect this closes: "Walk-forward validation
# (tools/revalidate_structural.py) runs on demand only. Nothing schedules it and
# no gate consumes its output." Both halves are addressed — this script is the
# schedule, and tools/check_revalidation.py is the gate that reads what it
# writes.
#
# The split matches ops/data-integrity.json and ops/ledger-revisions.txt: the
# database is ~4 GB and gitignored, so the answer is computed HERE, where the
# database lives, and committed as ops/revalidation-status.json for CI to
# verify. A runner cannot re-derive it and must not pretend to.
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO" || exit 2
OUT="$REPO/ops/revalidation-status.json"
TMP="$OUT.tmp"

# THIS SCRIPT OWNS ITS OWN SCHEDULE AND ITS OWN LOG, because its task carries
# neither.
#
# ops/com.signaldeck.revalidation.plist declares Day=1 (MONTHLY) plus
# StandardOutPath/StandardErrorPath. The LIVE task predates both: it has a DAILY
# trigger and a bare bash action with no -lc redirect, so logs/revalidation.out.log
# has never existed and a monthly gate's refusals -- "REVALIDATION FAILED",
# "produced an unusable snapshot" -- printed to a console that does not exist
# under S4U.
#
# Re-registering the task needs elevation (Set-ScheduledTask and schtasks /Change
# both return Access is denied; both were tried). Neither of these does.
# ops/fix-task-logging.ps1 still ships for an operator who wants the registration
# itself corrected; until then the daily trigger is harmless and the output lands
# in a file.
#
# -force runs it regardless of the day, for an operator invoking it deliberately.
LOG="$REPO/logs/revalidation.out.log"
mkdir -p "$REPO/logs"
exec >> "$LOG" 2>&1

if [ "${1:-}" != "-force" ] && [ "$(date -u +%d)" != "01" ]; then
  echo "=== revalidation SKIPPED $(date -u +%Y-%m-%dT%H:%M:%SZ): monthly job, today is not the 1st ==="
  echo "    (the live task fires daily; the plist declares Day=1. Pass -force to run now.)"
  exit 0
fi

PY="${SIGNALDECK_PYTHON:-python}"
# `command -v` alone matches the Microsoft Store alias stub, which resolves and
# then prints "Python was not found" -- the trap ops/accuracy-registry.sh and
# ops/signaldeck-backup-offline.sh both document. Probe by RUNNING it.
for cand in "$PY" python3 python py; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import sys' >/dev/null 2>&1; then
    PY="$cand"; break
  fi
done

echo "=== structural revalidation $(date -u +%Y-%m-%dT%H:%M:%SZ) ==="

# Write to a temp file and move it into place only on success. A crashed run
# must leave the PREVIOUS answer intact rather than a truncated one: a
# half-written snapshot would pass a file-exists check while carrying nothing,
# which is the failure mode the gate is there to prevent.
if ! "$PY" tools/revalidate_structural.py --json "$TMP"; then
    echo "REVALIDATION FAILED — leaving the previous snapshot in place" >&2
    rm -f "$TMP"
    exit 1
fi

if ! "$PY" -c "import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if d.get('generated') and d.get('arms') else 1)" "$TMP"; then
    echo "REVALIDATION produced an unusable snapshot — not replacing the previous one" >&2
    rm -f "$TMP"
    exit 1
fi

mv -f "$TMP" "$OUT"
echo "wrote $OUT"

# Read it back through the gate, so a scheduled run that produces something the
# gate would reject fails HERE rather than in CI a day later.
"$PY" tools/check_revalidation.py
