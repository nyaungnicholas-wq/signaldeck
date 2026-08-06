# DRAFT ONLY -- DO NOT EXECUTE AGAINST ANYTHING BUT A SCRATCH COPY.
# This is the proof harness for drafts/patches/refresh-atomicity.patch.
# It builds a slim throwaway DB from the READ-ONLY backup (mkfixture below) and
# exercises the patched script against THAT. It must never be pointed at
# data/signaldeck.db. It reaches no daemon: `prune-only` returns from step 0,
# before any sd_svc_start/sd_svc_stop call.
#
# Build the fixture first:
#   python refresh-atomicity-fixture.py <scratch>/fixture/data/signaldeck.db
#   cp ops/signaldeck-refresh.sh ops/lib-portable.sh <scratch>/fixture/ops/
#   mkdir -p <scratch>/fixture/logs && bash refresh-atomicity-check.sh
set -u
T="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/fixture"
DB="$T/data/signaldeck.db"
SH="$T/ops/signaldeck-refresh.sh"
fail=0
act(){ sqlite3 "$DB" "SELECT COUNT(*) FROM symbols WHERE active=1;" | tr -d '\r'; }
mark(){ sqlite3 "$DB" "SELECT COALESCE(v,'') FROM meta WHERE k='sweep_open';" | tr -d '\r'; }
ok(){ if [ "$2" = "$3" ]; then echo "  PASS $1 ($2)"; else echo "  FAIL $1: got '$2' want '$3'"; fail=1; fi; }

# The exact widening transaction from the patch.
open_universe(){
  sqlite3 "$DB" "PRAGMA busy_timeout=120000;
BEGIN IMMEDIATE;
INSERT INTO meta(k,v) VALUES('sweep_open', CAST(strftime('%s','now') AS TEXT))
  ON CONFLICT(k) DO UPDATE SET v=excluded.v;
UPDATE symbols SET active=1 WHERE market='stocks';
COMMIT;"
}

echo "1. baseline (lean, as the 13:10 backup had it)"
ok "no marker" "$(mark)" ""
base=$(act); echo "  baseline active=$base"

echo "2. widen: marker and active=1 commit together"
open_universe
ok "active widened" "$(act)" "2950"
[ -n "$(mark)" ] && echo "  PASS marker set ($(mark))" || { echo "  FAIL marker not set"; fail=1; }

echo "3. simulate the 2026-08-06 kill: nothing runs the prune, then daemon-guard ticks"
bash "$SH" prune-only 0
ok "universe repaired to the lean set" "$(act)" "328"
ok "marker cleared" "$(mark)" ""

echo "4. prune-only is a no-op when the universe is not open"
before=$(act)
bash "$SH" prune-only 0
ok "unchanged" "$(act)" "$before"

echo "5. a LIVE sweep must not be pruned out from under itself (min-age gate)"
open_universe
bash "$SH" prune-only 7200
ok "left open, marker is fresh" "$(act)" "2950"
[ -n "$(mark)" ] && echo "  PASS marker still set" || { echo "  FAIL marker cleared"; fail=1; }

echo "6. ...but a HUNG sweep is repaired once it passes the ceiling"
sqlite3 "$DB" "UPDATE meta SET v=CAST(strftime('%s','now')-9000 AS TEXT) WHERE k='sweep_open';"
bash "$SH" prune-only 7200
ok "repaired" "$(act)" "328"
ok "marker cleared" "$(mark)" ""

echo "7. corrupt marker repairs rather than blocks (fail-safe direction)"
open_universe
sqlite3 "$DB" "UPDATE meta SET v='garbage' WHERE k='sweep_open';"
bash "$SH" prune-only 7200
ok "repaired despite unparseable marker" "$(act)" "328"

[ "$fail" = 0 ] && echo "ALL CHECKS PASSED" || echo "SOME CHECKS FAILED"
exit "$fail"
