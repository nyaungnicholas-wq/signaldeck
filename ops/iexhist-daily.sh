#!/bin/bash
# Daily update of the IEX-only PUBLIC instance: decode any trading day that has
# no CSV yet (newest 10 listed days), then rebuild the public database's bars.
# Safe to re-run; a failed day leaves no CSV and the loader refuses a partial
# day. Data provided for free by IEX. https://www.iex.io/legal/hist-data-terms
#
#   SD_PUBLIC_ROOT   the public instance's root (holds bin/ and signaldeck/data)
#   SD_IEX_DIR       where the per-day CSVs live
set -u
ROOT="${SD_PUBLIC_ROOT:?set SD_PUBLIC_ROOT}"
DIR="${SD_IEX_DIR:?set SD_IEX_DIR}"
LOG="$ROOT/signaldeck/logs/iexhist-daily.log"
say() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" | tee -a "$LOG"; }
LIST="$(mktemp)"; trap 'rm -f "$LIST"' EXIT
if ! curl -sfL --max-time 120 -o "$LIST" "https://iextrading.com/api/1.0/hist"; then
  say "FAILED: could not read the IEX file list"; exit 1
fi
python - "$LIST" "$DIR" <<'PY' > "$LIST.q" || { say "FAILED: could not parse the IEX file list"; exit 1; }
import json, os, sys
d = json.load(open(sys.argv[1]))
for day in sorted(d)[-10:]:
    tops = [f for f in d[day] if f.get("feed") == "TOPS"]
    if tops and not os.path.exists(os.path.join(sys.argv[2], day + ".csv")):
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
rm -f "$LIST.q"
if ! "$ROOT/bin/iexhist-load.exe" -dir "$DIR" -db "$ROOT/signaldeck/data/signaldeck.db" >>"$LOG" 2>&1; then
  say "FAILED: load"; exit 1
fi
say "IEXHIST DAILY OK (failed days this run: $failed)"
exit $failed
