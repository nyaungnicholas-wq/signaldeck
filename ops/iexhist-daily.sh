#!/bin/bash
# Daily update of the IEX-only PUBLIC instance: decode every listed trading day
# from the oldest CSV on that has no CSV yet (so an outage of any length heals
# with no hole), then rebuild the public database's bars.
# Safe to re-run; a failed day leaves no CSV and the loader refuses a partial
# day. Data provided for free by IEX. https://www.iex.io/legal/hist-data-terms
#
#   SD_PUBLIC_ROOT   the public instance's root (holds bin/ and signaldeck/data)
#   SD_IEX_DIR       where the per-day CSVs live
#   SD_PYTHON        python interpreter (default: the repo venv beside this script)
set -u
ROOT="${SD_PUBLIC_ROOT:?set SD_PUBLIC_ROOT}"
DIR="${SD_IEX_DIR:?set SD_IEX_DIR}"
LOG="$ROOT/signaldeck/logs/iexhist-daily.log"
say() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" | tee -a "$LOG"; }
PY="${SD_PYTHON:-$(cd "$(dirname "$0")/.." && pwd)/.venv/Scripts/python.exe}"
# One run at a time: two runs would share a day's .tmp file.
LOCKDIR="$DIR/.daily.lock"
if ! mkdir "$LOCKDIR" 2>/dev/null; then say "another run holds $LOCKDIR; not starting"; exit 1; fi
LIST="$(mktemp)"; trap 'rm -f "$LIST" "$LIST.q"; rmdir "$LOCKDIR" 2>/dev/null' EXIT
if ! curl -sfL --max-time 120 -o "$LIST" "https://iextrading.com/api/1.0/hist"; then
  say "FAILED: could not read the IEX file list"; exit 1
fi
"$PY" - "$LIST" "$DIR" <<'PY' > "$LIST.q" || { say "FAILED: could not parse the IEX file list"; exit 1; }
import json, os, sys
d = json.load(open(sys.argv[1]))
have = sorted(f[:8] for f in os.listdir(sys.argv[2]) if len(f) == 12 and f.endswith(".csv") and f[:8].isdigit())
if not have:
    sys.exit("no day files yet: run the backfill first")
for day in sorted(d):
    tops = [f for f in d[day] if f.get("feed") == "TOPS"]
    if day > have[0] and tops and not os.path.exists(os.path.join(sys.argv[2], day + ".csv")):
        print(day, tops[0]["link"])
PY
failed=0
while read -r day url; do
  [ -n "$day" ] || continue
  if "$ROOT/bin/iexhist-bars.exe" -url "$url" -out "$DIR/$day.tmp" 2>>"$LOG"; then
    mv "$DIR/$day.tmp" "$DIR/$day.csv"; say "ok $day"
  else
    rm -f "$DIR/$day.tmp"; say "FAILED $day"; failed=1
  fi
done < <(tr -d '\r' < "$LIST.q")
if ! "$ROOT/bin/iexhist-load.exe" -dir "$DIR" -db "$ROOT/signaldeck/data/signaldeck.db" >>"$LOG" 2>&1; then
  say "FAILED: load"; exit 1
fi
say "IEXHIST DAILY OK (failed days this run: $failed)"
exit $failed
