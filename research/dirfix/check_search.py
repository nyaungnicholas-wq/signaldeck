"""Acceptance test for search.py. The point of this file is the HOLDOUT GUARD:
hundreds of search loops are exactly how a fake 60% gets manufactured, so the
search must be structurally incapable of touching the sealed period."""
import json, numpy as np, pandas as pd, search

FAIL = []
def ck(n, c):
    print(("  OK   " if c else "  FAIL ") + n)
    if not c: FAIL.append(n)

hold = json.load(open("HOLDOUT.json"))
ck("search.SPLIT matches the sealed HOLDOUT.json", str(search.SPLIT)[:10] == hold["split"])

# --- the guard must REFUSE data at or past the split
days = pd.date_range("2024-01-01", "2026-01-01", freq="B")
leak = pd.DataFrame({"day": days, "symbol_id": 1, "prob": 0.6, "y": 1.0})
clean = leak[leak.day < hold["split"]].copy()
raised = False
try: search.assert_search_only(leak)
except Exception: raised = True
ck("assert_search_only RAISES on a frame containing holdout days", raised)
ok = True
try: search.assert_search_only(clean)
except Exception: ok = False
ck("assert_search_only passes a clean search-period frame", ok)

# --- coverage sweep: fewer calls at higher conviction, and it must report coverage
rng = np.random.default_rng(0)
n, d = 20000, np.repeat(pd.date_range("2020-01-01", periods=100, freq="B"), 200)
sig = rng.normal(size=n)
y = (sig + rng.normal(scale=1.5, size=n) > 0).astype(float)   # signal is real but noisy
prob = 1 / (1 + np.exp(-sig))
pred = pd.DataFrame({"day": d, "symbol_id": np.tile(np.arange(200), 100), "prob": prob, "y": y})
rows = search.sweep_coverage(pred, {"id": "t"})
ck("sweep_coverage returns several coverage levels", len(rows) >= 4)
df = pd.DataFrame(rows).sort_values("coverage")
ck("every row reports coverage, n, acc, null, skill, ci_lo, ci_hi",
   all(c in df.columns for c in ("coverage", "n", "acc", "null", "skill", "ci_lo", "ci_hi")))
ck("lower coverage keeps strictly fewer rows", df.n.is_monotonic_increasing)
ck("accuracy RISES as coverage falls on a genuinely predictive signal",
   df.iloc[0].acc > df.iloc[-1].acc)
ck("coverage values are fractions in (0,1]", df.coverage.between(0, 1).all())

# --- deflation: the reported bar must rise with the number of trials
b10, b500 = search.deflated_threshold(0.01, 10), search.deflated_threshold(0.01, 500)
ck("deflated_threshold grows with trial count (%.4f -> %.4f)" % (b10, b500), b500 > b10 > 0)

print("\nALL CHECKS PASSED" if not FAIL else "\nFAILED:\n  " + "\n  ".join(FAIL))
raise SystemExit(1 if FAIL else 0)
