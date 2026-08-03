#!/bin/bash
# Nightly bias regression — re-run every leakage / look-ahead / no-bias test, and
# re-check the honesty invariants against the LIVE database.
#
# The bias tests already run on commit. That catches a code change that breaks
# them; it cannot catch the other direction, which is the one that actually
# happens: the code stays still while the DATA moves underneath it, until an
# assumption a test encodes ("forward windows never share a day", "no forecast
# rests on a contaminated window") quietly stops describing reality. So this runs
# nightly, and it runs the same tests against live state.
#
# Read-only with respect to the live database: the Go tests use isolated temp
# stores, and the live checks open the DB in mode=ro so they are safe while the
# daemon is writing.

set -uo pipefail

SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"
DAEMON="$SD/daemon"
LOG="$SD/logs/nightly-bias.log"
DB="$SD/data/signaldeck.db"
GO="$HOME/.local/go-sdk/go/bin/go"

exec >> "$LOG" 2>&1
echo "──────── $(date '+%Y-%m-%dT%H:%M:%S') nightly bias regression ────────"

fail=0

# ── 1. The bias-discipline test suite ──────────────────────────────────────
# Every package whose tests encode a no-lookahead, purged-split, embargo,
# non-overlap or cost-net invariant. Run verbosely and grep for the named
# guarantees, so a test being RENAMED OR DELETED is a failure too — a silently
# removed invariant is worse than a failing one.
BIAS_PKGS=(
  ./internal/alphax ./internal/forecast ./internal/gbm ./internal/meanrev
  ./internal/structregime ./internal/volregime ./internal/pressure
  ./internal/regimecond ./internal/ensemble ./internal/signalbt
  ./internal/distribution ./internal/featureredundancy ./internal/canary
  ./internal/datasetver ./internal/pricecheck ./internal/backtest
)
cd "$DAEMON" || exit 1
if ! "$GO" test "${BIAS_PKGS[@]}" 2>&1 | tail -30; then
  echo "FAIL: bias-discipline suite did not pass"
  fail=1
fi

# The named invariants must still exist. Counting them makes deletion visible.
n_lookahead=$("$GO" test "${BIAS_PKGS[@]}" -run 'Lookahead|LookAhead|Leak|Purge|Embargo|Overlap|Causal' -v 2>/dev/null | grep -c '^=== RUN')
echo "no-lookahead / purge / embargo tests executed: $n_lookahead"
if [ "$n_lookahead" -lt 10 ]; then
  echo "FAIL: expected at least 10 bias-invariant tests, found $n_lookahead — has one been renamed or deleted?"
  fail=1
fi

# ── 2. Live-data invariants ────────────────────────────────────────────────
# The assumptions the tests encode, checked against what is actually in the DB
# tonight. These are the ones that rot without any code changing.
if [ -f "$DB" ]; then
  python3 - "$DB" <<'PY'
import sqlite3, sys

db = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
q = lambda s, *a: db.execute(s, a).fetchone()
problems = []

# A resolved outcome must never be stamped before the bar it grades. This is the
# look-ahead check that matters most, and it is cheap.
row = q("""SELECT COUNT(*) FROM prediction_outcomes
           WHERE resolved_at IS NOT NULL AND resolved_at < ts""")
if row[0]:
    problems.append(f"{row[0]} outcomes resolved BEFORE their prediction timestamp")

# Independent-observation discipline: report the inflation factor so a change in
# write cadence cannot silently turn 8k observations into 500k.
raw = q("SELECT COUNT(*) FROM prediction_outcomes WHERE resolved_at IS NOT NULL")[0]
ind = q("""SELECT COUNT(*) FROM (SELECT symbol_id, horizon, ts/86400 AS d
           FROM prediction_outcomes WHERE resolved_at IS NOT NULL
           GROUP BY symbol_id, horizon, d)""")[0]
factor = raw / ind if ind else 0
print(f"resolved outcomes: {raw} raw → {ind} independent (inflation {factor:.1f}x)")
if factor > 80:
    problems.append(f"pooling inflation is {factor:.1f}x — an accuracy computed on raw rows would be badly overstated")

# Split contamination: a forecast resting on a window with an impossible daily
# move is the bug that produced garbage live forecasts once already.
row = q("""SELECT COUNT(*) FROM (
             SELECT symbol_id, close, LAG(close) OVER (PARTITION BY symbol_id ORDER BY ts) prev
             FROM bars WHERE tf='1d' AND ts > strftime('%s','now','-400 days'))
           WHERE prev > 0 AND ABS(close/prev - 1) > 0.65""")
if row[0]:
    print(f"note: {row[0]} implausible daily moves in the last 400d — predictors refuse these windows by design")

# A return-distribution forecast must never claim skill it did not measure.
try:
    row = q("SELECT COUNT(*) FROM return_forecasts WHERE skill IS NOT NULL AND graded_n < 30")[0]
    if row:
        problems.append(f"{row} return forecasts report skill on fewer than 30 graded pairs")
    tot = q("SELECT COUNT(*) FROM return_forecasts")[0]
    pos = q("SELECT COUNT(*) FROM return_forecasts WHERE skill > 0")[0]
    print(f"return distributions: {tot} stored, {pos} with positive conditioning skill")
except sqlite3.OperationalError:
    print("return_forecasts not present yet (worker has not run)")

# A canary must never be recorded as promoted below its own floors.
try:
    row = q("""SELECT COUNT(*) FROM canary_trials
               WHERE decision='promote' AND (ch_n < 30 OR ch_lower <= baseline)""")[0]
    if row:
        problems.append(f"{row} canary promotions below the observation floor or the naive baseline")
except sqlite3.OperationalError:
    print("canary_trials not present yet (worker has not run)")

# A retired model must not still be emitting.
try:
    row = q("SELECT COUNT(*) FROM model_health WHERE verdict='retired' AND emitting=1")[0]
    if row:
        problems.append(f"{row} retired models are still emitting")
except sqlite3.OperationalError:
    pass

if problems:
    print("LIVE-DATA BIAS PROBLEMS:")
    for p in problems:
        print(f"  - {p}")
    sys.exit(1)
print("live-data invariants: all clean")
PY
  [ $? -ne 0 ] && fail=1
else
  echo "note: live DB not found at $DB — Go suite only"
fi

if [ "$fail" -ne 0 ]; then
  echo "ACTION REQUIRED: nightly bias regression found a problem (see above)"
  sd_notify "SignalDeck" "Nightly bias regression failed — see logs/nightly-bias.log" 2>/dev/null
  exit 1
fi
echo "nightly bias regression: PASS"
