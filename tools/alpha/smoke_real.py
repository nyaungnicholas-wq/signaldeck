"""Run the labeler over real SignalDeck daily bars and report the outcome mix.

Read-only. Proves the labeler survives real data, and that the label
distribution is sane rather than degenerate.
"""
import sqlite3
import numpy as np
from labels import ewma_sigma, triple_barrier, sample_weights

DB = r"C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data\signaldeck.db"
UP, DN, VB = 2.0, 1.0, 20

con = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
cur = con.cursor()

# the 200 deepest daily series
syms = [r[0] for r in cur.execute(
    "SELECT symbol_id FROM bars WHERE tf='1d' GROUP BY symbol_id "
    "HAVING COUNT(*) >= 1890 ORDER BY COUNT(*) DESC LIMIT 200")]
print(f"symbols: {len(syms)}")

tot = {1: 0, -1: 0, 0: 0, -128: 0}
all_w, n_ev, bad = [], 0, 0

for sid in syms:
    rows = list(cur.execute(
        "SELECT ts,high,low,close FROM bars WHERE tf='1d' AND symbol_id=? ORDER BY ts", (sid,)))
    if len(rows) < VB + 50:
        continue
    a = np.array(rows, dtype=float)
    h, l, c = a[:, 1], a[:, 2], a[:, 3]
    if np.any(~np.isfinite(c)) or np.any(c <= 0):
        bad += 1
        continue
    sig = ewma_sigma(c, span=100)
    r = triple_barrier(h, l, c, sig, up_mult=UP, dn_mult=DN, vbars=VB)
    lab = r["label"]
    for k in tot:
        tot[k] += int((lab == k).sum())
    ok = lab != -128
    idx = np.flatnonzero(ok)
    if idx.size:
        w = sample_weights(r["touch_ret"][idx], idx, r["touch_idx"][idx], len(c))
        all_w.append(w)
        n_ev += idx.size

res = tot[1] + tot[-1] + tot[0]
print(f"skipped (bad price data): {bad}")
print(f"resolved events: {res:,}   excluded (truncated): {tot[-128]:,}")
print(f"  +1 upper : {tot[1]:>9,}  ({100*tot[1]/res:5.2f}%)")
print(f"  -1 lower : {tot[-1]:>9,}  ({100*tot[-1]/res:5.2f}%)")
print(f"   0 vert  : {tot[0]:>9,}  ({100*tot[0]/res:5.2f}%)")

w = np.concatenate(all_w)
print(f"\nsample weights: n={len(w):,} mean={w.mean():.4f} "
      f"min={w.min():.4f} max={w.max():.4f}")
print(f"  finite: {np.all(np.isfinite(w))}   non-negative: {np.all(w >= 0)}")

# A 2:1 barrier on a near-random walk should hit the near (lower) barrier more
# often than the far (upper) one. If +1 dominates, the walk is wrong.
print("\nsanity:")
print(f"  lower/upper ratio = {tot[-1]/max(tot[1],1):.2f} "
      f"(expect >1 for a 2sigma/1sigma barrier pair)")
assert tot[-1] > tot[1], "2:1 barriers must hit the near side more often"
assert tot[-128] > 0, "the tail of every series must be excluded"
assert abs(w.mean() - 1.0) < 1e-6, "weights must normalize to mean 1"
print("\nreal-data smoke passed")
con.close()
