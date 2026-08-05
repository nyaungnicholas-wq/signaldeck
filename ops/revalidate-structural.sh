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

PY="${SIGNALDECK_PYTHON:-python}"
command -v "$PY" >/dev/null 2>&1 || PY=python3

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
