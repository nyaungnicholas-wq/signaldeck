"""Regression gate for ops/schema_inventory.py.

WHY THIS EXISTS. ops/eighty-loop.ps1 tells the hypothesis generator what is in
the database. That description used to be typed by hand, and it drifted twice.
The second drift was measured 2026-08-13: it claimed `filings` held "ONLY
2026-02-05..now" against a table spanning 1999-08-10..2026-08-12, and gave no
date range at all for prediction_outcomes -- the documented "safest label
source", which holds 41 distinct label days and only the '1d' and '1w'
horizons. The generator therefore proposed 21- and 63-trading-day mechanisms
whose labels do not exist, and 54 of the last 60 research cycles died "data
cannot support this hypothesis" with 0 KEEPs in 624 cycles.

A hand-typed number is a claim about the database that nothing checks. This
test recomputes every count and range from the live DB and fails if the
generated inventory disagrees, so the block cannot silently drift again.

Run: python ops/test_schema_inventory.py    (exit 0 = inventory matches live DB)


Contract under test:
  python ops/schema_inventory.py            -> inventory text on stdout, exit 0
  last non-blank line must be '# END SCHEMA INVENTORY'

The point of this check is that the inventory must match the LIVE database,
not a hand-maintained string. So we recompute the ground truth here
independently and assert the script's output agrees.
"""
import re
import sqlite3
import subprocess
import sys
from datetime import datetime, timedelta, timezone

EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)
from pathlib import Path

# This test lives in ops/, so the repo root is its parent's parent.
REPO = Path(__file__).resolve().parent.parent

DB = REPO / "data" / "signaldeck.db"
SCRIPT = REPO / "ops" / "schema_inventory.py"

# table -> time column (epoch ints unless marked 'str')
TABLES = {
    "bars": ("ts", "epoch"),
    "symbols": ("added_at", "epoch"),
    "prediction_outcomes": ("ts", "epoch"),
    "insider_trades": ("filed_ts", "epoch"),
    "filings": ("filed_ts", "epoch"),
    "short_volume": ("day", "str"),
    "news": ("ts", "epoch"),
    "sentiment_features": ("day", "str"),
    "stocktwits_sentiment": ("ts", "epoch"),
    "macro_series": ("ts", "epoch"),
    "fundamentals": ("fetched_at", "epoch"),
    "inst_holdings": ("period", "str"),
    "anomalies": ("ts", "epoch"),
}

fail = []


def check(cond, msg):
    if not cond:
        fail.append(msg)


assert SCRIPT.exists(), f"missing {SCRIPT}"
assert DB.exists(), f"missing {DB}"

proc = subprocess.run(
    [sys.executable, str(SCRIPT)],
    capture_output=True, text=True, cwd=str(REPO), timeout=600,
)
check(proc.returncode == 0, f"exit {proc.returncode}; stderr={proc.stderr[-500:]}")
out = proc.stdout
check(bool(out.strip()), "empty stdout")

# --- completion sentinel: guards against a truncated / half-written block ---
lines = [ln for ln in out.splitlines() if ln.strip()]
check(bool(lines) and lines[-1].strip() == "# END SCHEMA INVENTORY",
      f"last non-blank line is {lines[-1].strip()!r}, expected '# END SCHEMA INVENTORY'")

# --- ground truth straight from the live DB ---
conn = sqlite3.connect(f"file:{DB.as_posix()}?mode=ro", uri=True)
cur = conn.cursor()


def iso(v):
    if isinstance(v, str):
        return v[:10]
    # macro_series.ts goes back to 1947 (negative epoch); fromtimestamp raises
    # OSError on Windows for those, so add a timedelta to the epoch instead.
    return (EPOCH + timedelta(seconds=int(v))).date().isoformat()


for tbl, (col, kind) in TABLES.items():
    check(tbl in out, f"table {tbl} missing from inventory")
    if tbl not in out:
        continue
    line = next((ln for ln in lines if ln.lstrip("- ").startswith(tbl)), None)
    check(line is not None, f"no inventory line starts with {tbl}")
    if line is None:
        continue

    n = cur.execute(f"SELECT COUNT(*) FROM {tbl}").fetchone()[0]
    mn, mx = cur.execute(f"SELECT MIN({col}), MAX({col}) FROM {tbl}").fetchone()

    # row count must appear, within 2% (the DB is live and grows during the run)
    nums = [int(x.replace(",", "")) for x in re.findall(r"\b[\d,]{2,}\b", line)
            if x.replace(",", "").isdigit()]
    close = [x for x in nums if n * 0.98 <= x <= n * 1.02]
    check(bool(close), f"{tbl}: no row count near live {n:,} in line: {line.strip()!r}")

    # date range must appear and must match the live min/max (not a stale literal)
    if mn is not None:
        want_lo, want_hi = iso(mn), iso(mx)
        dates = re.findall(r"\d{4}-\d{2}-\d{2}", line)
        check(want_lo in dates,
              f"{tbl}: live start {want_lo} not in line: {line.strip()!r}")
        check(want_hi in dates,
              f"{tbl}: live end {want_hi} not in line: {line.strip()!r}")

# --- the specific defect this fix exists to close ---
# prediction_outcomes is the label source. Its horizons and its (short) window
# must both be stated, or the generator proposes 21d/63d horizons that have no
# label at all -- which is what killed 54 of the last 60 research cycles.
po_horizons = [h for (h,) in cur.execute(
    "SELECT DISTINCT horizon FROM prediction_outcomes").fetchall()]
po_block = out[out.index("prediction_outcomes"):][:600] if "prediction_outcomes" in out else ""
for h in po_horizons:
    check(h in po_block,
          f"prediction_outcomes: available horizon {h!r} not stated in inventory")

po_days = cur.execute(
    "SELECT COUNT(DISTINCT date(ts,'unixepoch')) FROM prediction_outcomes").fetchone()[0]
check(str(po_days) in po_block,
      f"prediction_outcomes: distinct label-day count {po_days} not stated")

conn.close()

if fail:
    print("FAIL")
    for f in fail:
        print("  -", f)
    sys.exit(1)
print("OK: inventory matches live DB")
print(out[:400])
