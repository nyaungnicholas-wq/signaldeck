#!/bin/bash
# Daily structural-resolver liveness check.
#
# The regime-outcome worker logs "resolved 0" both when it is correctly waiting
# for a horizon to elapse and when it has stopped grading entirely. Until the
# first structural outcomes come due (2026-08-17 for the 21-day kinds,
# 2026-10-17 for trend63) those two states are indistinguishable from any log,
# any dashboard, and the worker's own summary — which is exactly the shape of
# failure that let two research-loop grid searches go unrecorded for nights.
#
# So the question gets asked on a schedule instead of by accident: is there a
# frozen call that has passed BOTH resolution gates and still carries no
# verdict? If yes, the resolver is not waiting, and this exits non-zero on the
# day it happened rather than whenever someone next queries the table.
#
# Read-only in the strongest sense: tools/structural_liveness.py opens the
# database with mode=ro and writes nothing at all — no dq event, no repair, no
# grade. The log below is the entire side effect.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
SD="$PWD"
# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"
DB="${SIGNALDECK_DB:-$SD/data/signaldeck.db}"
LOG="${SIGNALDECK_STRUCTURAL_LIVENESS_LOG:-$SD/logs/structural-liveness.log}"

mkdir -p "$(dirname "$LOG")"
out=$(mktemp)
trap 'rm -f "$out"' EXIT

py="$(sd_py)"
if [ -z "$py" ]; then
  echo "FATAL: no working python interpreter" >&2
  exit 2
fi

# Stdlib only, deliberately: this must run from whatever interpreter the box
# has, without the tools/ venv being present or current. A liveness check that
# can itself fail to start is not a liveness check.
"$py" "$SD/tools/structural_liveness.py" --db "$DB" > "$out" 2>&1
status=$?

{
  echo "-------- $(date '+%Y-%m-%dT%H:%M:%S') structural-liveness --------"
  cat "$out"
  echo "exit=$status"
} >> "$LOG"

cat "$out"
exit "$status"
