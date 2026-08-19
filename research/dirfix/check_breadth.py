"""Fast smoke check for breadth.py. Must fail if the sweep is fake or clipped."""
import math, breadth

rows = breadth.sweep(horizons=[21], ks=[10, 150], weightings=["conviction"],
                     betas=[False])
assert len(rows) == 2, f"expected 2 rows, got {len(rows)}"
by_k = {r["k"]: r for r in rows}
assert set(by_k) == {10, 150}, f"bad k set: {set(by_k)}"

for k, r in by_k.items():
    for col in ("h", "k", "weighting", "beta_neutral", "sharpe", "t_nw", "ann_ret",
                "max_dd", "beta", "turnover", "n_long_med", "pool_med", "acc", "null"):
        assert col in r, f"k={k} missing column {col}"
    assert math.isfinite(r["sharpe"]), f"k={k} sharpe not finite: {r['sharpe']}"
    assert math.isfinite(r["acc"]), f"k={k} acc not finite: {r['acc']}"
    assert 0.0 < r["acc"] < 1.0, f"k={k} acc out of range: {r['acc']}"
    # breadth must actually materialise, not silently clip to the pool
    assert r["n_long_med"] >= 0.9 * k, (
        f"k={k} only filled {r['n_long_med']} names (pool {r['pool_med']}) "
        "- breadth clipped, sweep is meaningless at this k")
    # both legs must FIT: if 2*k exceeds the pool, head(k) and tail(k) overlap and
    # the same symbol is held long and short. That looks identical to a real book.
    assert 2 * r["n_long_med"] <= r["pool_med"], (
        f"k={k} needs {2 * r['n_long_med']} names but the pool is {r['pool_med']} "
        "- the legs overlap, every number in this row is meaningless")

# the whole point: the two k values must differ, or the k axis is not wired up
assert by_k[10]["sharpe"] != by_k[150]["sharpe"], "k axis has no effect - not wired"
assert by_k[10]["n_long_med"] < by_k[150]["n_long_med"], "n_long does not track k"
print("OK", [(r["k"], round(r["sharpe"], 3), r["n_long_med"]) for r in rows])
