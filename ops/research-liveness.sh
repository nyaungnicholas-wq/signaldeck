#!/bin/bash
# Standalone daily research-loop liveness check.
#
# tools/research_liveness.py already ran inside ops/accuracy-registry.sh, before
# the grader. That placement is correct but insufficient: it means a violation is
# only ever discovered by an attempt to PUBLISH. worker_runs id=130100 and
# id=139413 narrated 48-rule grid searches over 163,216 and 166,285 observations
# with no judgment recorded, and that condition stood undetected for at least two
# nights for exactly this reason.
#
# So the check also runs on its own schedule (com.signaldeck.research-liveness.plist),
# with --emit-dq-event, so a failure lands in the data-quality stream on the day
# it happened instead of whenever someone next publishes.
#
# One-directional: the tool reads the database read-only and, on failure, writes
# a single dq_events row saying the check failed. It never writes a judgment,
# never repairs a ledger, never alters a number.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
SD="$PWD"
# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"
DB="${SIGNALDECK_DB:-$SD/data/signaldeck.db}"
LOG="${SIGNALDECK_LIVENESS_LOG:-$SD/logs/research-liveness.log}"

mkdir -p "$(dirname "$LOG")"
out=$(mktemp)
trap 'rm -f "$out"' EXIT

# No --emit-dq-event: that flag was never implemented. research_liveness.py
# only READS dq_events (the daemon is what writes research_sentinel rows), so
# argparse rejected it and this check exited 2 without ever running — on macOS
# too. The scheduled job has therefore never produced a verdict.
"$(sd_py)" "$SD/tools/research_liveness.py" --db "$DB" > "$out" 2>&1
status=$?

{
  echo "──────── $(date '+%Y-%m-%dT%H:%M:%S') research-liveness ────────"
  cat "$out"
  echo "exit=$status"
} >> "$LOG"

cat "$out"
exit "$status"
