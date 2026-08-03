#!/bin/bash
# The retention budget must adjust to the database and still catch a real
# regression. Both halves matter:
#
#   too tight  -> fires every day, and a permanently-red check trains you to
#                 ignore red. That is what the fixed 6144MB constant became once
#                 the DB grew from ~2GB to 3.3GB.
#   too loose  -> the 2026-07-26 pile-up (13GB of un-pruned raw snapshots) walks
#                 straight past it, which is the exact event it exists to catch.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Pull in ONLY the constants and the pure function, without running the script.
eval "$(sed -n '/^KEEP_RAW=/,/^KEEP_RAW_MAX=/p;/^GZ_RATIO_PCT=/p;/^BUDGET_FLOOR_MB=/p' "$SD/ops/signaldeck-backup-offline.sh")"
eval "$(sed -n '/^derive_budget_mb()/,/^}/p' "$SD/ops/signaldeck-backup-offline.sh")"

fails=0
check() { if [ "$2" = "1" ]; then echo "  ok   $1"; else echo "  FAIL $1"; fails=$((fails + 1)); fi; }

# 1. It tracks the database rather than sitting still.
small=$(derive_budget_mb 2000)
large=$(derive_budget_mb 4000)
check "budget grows with the database ($small -> $large)" "$([ "$large" -gt "$small" ] && echo 1 || echo 0)"
check "growth is proportional, not a step ($((large / small))x for 2x data)" \
  "$([ "$large" -ge $((small * 19 / 10)) ] && [ "$large" -le $((small * 21 / 10)) ] && echo 1 || echo 0)"

# 2. Steady state must sit COMFORTABLY inside it, or it false-alarms daily.
#    Real shape: 1 raw + 5 compressed.
for db in 1000 2000 3348 6000; do
  steady=$(( db + db * 5 * GZ_RATIO_PCT / 100 ))
  b=$(derive_budget_mb "$db")
  check "DB ${db}MB: steady state ${steady}MB fits under budget ${b}MB" \
    "$([ "$steady" -lt "$b" ] && echo 1 || echo 0)"
done

# 3. The 2026-07-26 incident must still trip it. That pile-up was ~13GB of raw
#    snapshots while the DB was ~2GB.
b2000=$(derive_budget_mb 2000)
check "the 13GB pile-up at a 2GB DB trips the budget (${b2000}MB)" \
  "$([ 13000 -gt "$b2000" ] && echo 1 || echo 0)"

# 4. A prune failure must be caught PROMPTLY — within a few stray generations,
#    not after a dozen. One stray generation is one extra raw copy.
db=3348
b=$(derive_budget_mb "$db")
steady=$(( db + db * 5 * GZ_RATIO_PCT / 100 ))
strays=0
cur=$steady
while [ "$cur" -le "$b" ] && [ "$strays" -lt 20 ]; do
  cur=$(( cur + db )); strays=$(( strays + 1 ))
done
check "a prune failure trips within 3 stray generations (took $strays)" \
  "$([ "$strays" -le 3 ] && echo 1 || echo 0)"

# 5. A tiny or missing database must not derive an absurd budget.
check "a 0MB database falls back to the floor (${BUDGET_FLOOR_MB}MB)" \
  "$([ "$(derive_budget_mb 0)" = "$BUDGET_FLOOR_MB" ] && echo 1 || echo 0)"
check "a 10MB database also uses the floor" \
  "$([ "$(derive_budget_mb 10)" = "$BUDGET_FLOOR_MB" ] && echo 1 || echo 0)"

# 6. The derivation is inspectable — a self-adjusting limit nobody can read is
#    just a different magic number.
out="$(bash "$SD/ops/signaldeck-backup-offline.sh" --explain-budget 2>/dev/null)"
check "--explain-budget reports the derivation" \
  "$(printf '%s' "$out" | grep -q 'derived budget' && echo 1 || echo 0)"
check "--explain-budget separates ad-hoc from managed" \
  "$(printf '%s' "$out" | grep -q 'never rotated' && echo 1 || echo 0)"

if [ "$fails" -gt 0 ]; then echo "$fails check(s) failed"; exit 1; fi
echo "all retention-budget checks passed"
