"""SD-30 read-only probe: does the part of the label window that is already
visible at prediction time (settled base close -> prediction ts) leak into the
call? Leakage signatures:
  1. the call's direction agrees with the visible partial move far above 50%;
  2. accuracy rises with the share of the window already elapsed;
  3. accuracy on calls that DISAGREE with the visible move collapses.
Rows issued from pipeline.labelBaseSinceTs (2026-10-04, SD-30 fix) are labelled
from the issue day's own bar, whose window opens at that bar's CLOSE: a call
issued before it has nothing visible, and after it only the after-hours move
(the accepted residual). Old and new rows are reported separately.
Stdlib only; opens the DB read-only.
Usage: python tools/sd30_label_window_probe.py [path/to/signaldeck.db] [--kept]
  --kept  only the row the grader keeps: the latest call per (symbol, base day)"""
import sqlite3
from collections import defaultdict

import os
import pathlib
import sys

_args = [a for a in sys.argv[1:] if not a.startswith("--")]
KEPT = "--kept" in sys.argv[1:]
_db = _args[0] if _args else os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data", "signaldeck.db")
DB = pathlib.Path(_db).resolve().as_uri() + "?mode=ro"
SETTLED_SINCE = 1788807600  # pipeline/settledbase.go settledBaseSinceTs
LABEL_SINCE = 1791072000    # pipeline/settledbase.go labelBaseSinceTs (SD-30 fix)
SETTLE = {"crypto": 24 * 3600, "stocks": 22 * 3600}
CLOSE = {"crypto": 24 * 3600, "stocks": 16 * 3600}  # bar stamp -> its close (ET midnight stamps)

con = sqlite3.connect(DB, uri=True)
con.execute("PRAGMA query_only=1")
rows = con.execute("""
  SELECT p.symbol_id, s.market, p.ts, p.prob, p.up, p.fwd_return
  FROM prediction_outcomes p JOIN symbols s ON s.id = p.symbol_id
  WHERE p.horizon='1d' AND p.resolved_at IS NOT NULL AND p.up IS NOT NULL
    AND p.ts >= ?""", (SETTLED_SINCE,)).fetchall()

def settled_base(sym, market, ts):
    for bts, close in con.execute(
            "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 3",
            (sym, ts)):
        if ts >= bts + SETTLE[market]:
            return bts, close
    return None

def own_bar(sym, ts):
    """The issue day's own bar: the base for rows from LABEL_SINCE."""
    return con.execute("SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 1",
                       (sym, ts)).fetchone()

def price_at(sym, ts):
    r = con.execute("SELECT close, ts FROM bars WHERE symbol_id=? AND tf='1m' AND ts<=? ORDER BY ts DESC LIMIT 1",
                    (sym, ts)).fetchone()
    if r and ts - r[1] <= 900:  # a minute bar within 15 min of the call
        return r[0]
    return None

stats = defaultdict(lambda: [0, 0, 0, 0, 0])  # n, hits, call agrees with visible, n_visible, OUTCOME matches visible
by_elapsed = defaultdict(lambda: [0, 0])
dis = defaultdict(lambda: [0, 0])
based = []
for sym, market, ts, prob, up, fwd in rows:
    new = ts >= LABEL_SINCE
    b = own_bar(sym, ts) if new else settled_base(sym, market, ts)
    if b:
        based.append((sym, market, ts, prob, up, b[0], b[1], new))
if KEPT:  # the grader keeps the latest call per symbol per base day
    latest = {}
    for r in based:
        k = (r[0], r[5])
        if k not in latest or r[2] > latest[k][2]:
            latest[k] = r
    based = list(latest.values())
for sym, market0, ts, prob, up, bts, bclose, new in based:
    market = market0 + (" new" if new else " old")
    call_up = prob >= 0.5
    hit = call_up == bool(up)
    st = stats[market]
    st[0] += 1
    st[1] += hit
    opens = bts + (CLOSE if new else SETTLE)[market0]  # when the label window opened
    if new and ts < opens:
        continue  # issued before its own bar closed: nothing of the window is visible
    px = price_at(sym, ts)
    if px is None or bclose <= 0:
        continue
    visible = px / bclose - 1
    if visible == 0:
        continue
    st[3] += 1
    agrees = call_up == (visible > 0)
    st[2] += agrees
    st[4] += bool(up) == (visible > 0)  # the leak itself (93.8% on kept stocks rows, 10-02)
    frac = min(0.999, (ts - opens) / 86400.0)
    k = (market, int(frac * 4))  # quarter of the day elapsed since settlement
    by_elapsed[k][0] += 1
    by_elapsed[k][1] += hit
    d = dis[(market, "agrees" if agrees else "disagrees")]
    d[0] += 1
    d[1] += hit

for m, (n, h, ag, nv, om) in sorted(stats.items()):
    print(f"{m}: n={n} acc={h/n:.3f} | with a visible pre-call move: n={nv} call agrees with it {ag/max(nv,1):.3f}"
          f" | outcome matches it {om/max(nv,1):.3f}")
for (m, q), (n, h) in sorted(by_elapsed.items()):
    print(f"  {m} elapsed quarter {q}: n={n} acc={h/n:.3f}")
for (m, a), (n, h) in sorted(dis.items()):
    print(f"  {m} call {a} with the visible move: n={n} acc={h/n:.3f}")
