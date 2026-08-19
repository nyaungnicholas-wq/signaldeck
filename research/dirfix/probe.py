"""Standalone reality check on the SEARCH period only. No lab.py dependency.
Question: does ANY simple edge exist, and at what coverage does it reach 60%?"""
import numpy as np, pandas as pd
SPLIT = "2025-03-01"
p = pd.read_parquet("panel.parquet")
p = p[p.day < SPLIT]
close = p.pivot(index="day", columns="symbol_id", values="close").sort_index()
vol   = p.pivot(index="day", columns="symbol_id", values="volume").sort_index()
dv = (close * vol).rolling(21).mean()
liq = (close >= 2.0) & (dv >= 1e6)
print("search period: %s..%s  %d days, %d symbols, liquid cells/day ~%d"
      % (close.index[0].date(), close.index[-1].date(), len(close), close.shape[1],
         int(liq.sum(axis=1).mean())))

def cov_acc(score, y, cov):
    """per-day top-`cov` fraction by |score|, accuracy of sign(score) vs y"""
    s = score.where(liq & y.notna()); yy = y.where(s.notna())
    conv = s.abs()
    keep = conv.rank(axis=1, pct=True, ascending=False) <= cov
    call = (s > 0).where(keep & s.notna())
    hit = (call == (yy > 0.5)).where(call.notna())
    per_day = hit.mean(axis=1)
    return float(hit.stack().mean()), float(per_day.count()), int(hit.count().sum())

for h in (1, 5):
    fwd = close.shift(-h) / close - 1
    y_abs = (fwd > 0).astype(float).where(fwd.notna())
    med = fwd.median(axis=1)
    y_rel = fwd.gt(med, axis=0).astype(float).where(fwd.notna())
    base = float(y_abs.where(liq).stack().mean())
    print("\n=== horizon %dd ===  absolute null (up-rate, liquid) = %.4f   relative null = %.4f"
          % (h, base, float(y_rel.where(liq).stack().mean())))
    mom21 = close.pct_change(21).shift(1)
    rev5  = -close.pct_change(5).shift(1)
    cs = lambda f: f.sub(f.mean(axis=1), axis=0)          # cross-sectional demean
    sigs = {"mom21_cs": cs(mom21), "rev5_cs": cs(rev5),
            "combo_cs": cs(mom21).rank(axis=1, pct=True) - 0.5 + 0.5*(cs(rev5).rank(axis=1, pct=True) - 0.5)}
    for name, s in sigs.items():
        out = []
        for cov in (1.0, 0.10, 0.02):
            a, d, n = cov_acc(s, y_rel, cov)
            out.append("cov%5.0f%% acc=%.4f (n=%d)" % (cov*100, a, n))
        print("  RELATIVE %-10s %s" % (name, "  ".join(out)))
