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
# verify (a figure-free stamp while the licence withholds the figures; see the
# LICENCE GATE below). A runner cannot re-derive it and must not pretend to.
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO" || exit 2
OUT="$REPO/ops/revalidation-status.json"
# The temp file holds the FULL figures (see LICENCE GATE below), so it is staged
# under gitignored data/, never beside $OUT in the public ops/ tree, where a
# killed run would leave it for a broad `git add` to commit.
TMP="$REPO/data/revalidation-status.json.tmp"

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
mkdir -p "$REPO/data"
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

# LICENCE GATE (23 Oct 2026). The arms and bands are accuracies computed from
# Alpaca market data, which Alpaca licenses for personal use only, derivations
# included, and ops/ is committed to a public repository. So the full snapshot
# always goes to data/revalidation-status.json (gitignored; tools/live_accuracy.py
# reads it there first), and ops/revalidation-status.json carries the figures
# only when the operator records Alpaca's written permission with
# SIGNALDECK_PUBLISH_DERIVED=1 (exactly "1", as ops/accuracy-registry.sh reads
# it). Otherwise ops/ gets a figure-free stamp: when it ran, and that the figures
# are withheld. tools/check_revalidation.py gates the stamp's age in CI, so the
# monthly cadence stays enforced without a figure being committed.
FULL="$REPO/data/revalidation-status.json"
mv -f "$TMP" "$FULL"
echo "wrote $FULL"
if [ "${SIGNALDECK_PUBLISH_DERIVED:-0}" = 1 ]; then
  cp -f "$FULL" "$OUT"
  echo "wrote $OUT (SIGNALDECK_PUBLISH_DERIVED=1)"
elif "$PY" - "$FULL" "$OUT" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    generated = json.load(f)["generated"]
stamp = {
    "generated": generated,
    "withheld": "licence: the revalidation's accuracies are computed from Alpaca market "
                "data and are not published (SIGNALDECK_PUBLISH_DERIVED is not 1). The "
                "full snapshot is kept privately in data/revalidation-status.json.",
    "full_snapshot": "data/revalidation-status.json",
}
with open(sys.argv[2], "w", encoding="utf-8", newline="\n") as f:
    json.dump(stamp, f, indent=2)
    f.write("\n")
PY
then
  echo "wrote $OUT (figure-free licence stamp)"
else
  echo "REVALIDATION stamp could not be written -- leaving the previous $OUT in place" >&2
  exit 1
fi

# Read it back through the gate, so a scheduled run that produces something the
# gate would reject fails HERE rather than in CI a day later. A stamp is checked
# together with the full snapshot it points at.
"$PY" tools/check_revalidation.py
