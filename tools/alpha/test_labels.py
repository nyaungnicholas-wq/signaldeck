"""Check for the triple-barrier labeler. Run: python test_labels.py

Every assert here encodes a way the labeler can be subtly wrong and still look
right on real data. Failures are specific on purpose.
"""
import numpy as np
from labels import triple_barrier, ewma_sigma, sample_weights

UP, DN, VB = 2.0, 1.0, 5   # +2sigma / -1sigma / 5-bar timeout


def bars(closes, highs=None, lows=None):
    c = np.asarray(closes, float)
    return (np.asarray(highs, float) if highs is not None else c,
            np.asarray(lows, float) if lows is not None else c,
            c)


def sig(n, v=0.01):
    return np.full(n, v)


def run(h, l, c, **kw):
    return triple_barrier(h, l, c, sig(len(c)), up_mult=UP, dn_mult=DN,
                          vbars=VB, **kw)


# 1. monotonic rise -> upper barrier. up=2*1% => +2% => 102 from 100.
h, l, c = bars([100, 100.5, 101, 102.5, 103, 104, 105, 106])
r = run(h, l, c)
assert r["label"][0] == 1, f"rise must label +1, got {r['label'][0]}"
assert r["touch_idx"][0] == 3, f"upper touched at bar 3, got {r['touch_idx'][0]}"
assert r["barrier"][0] == "up"

# 2. monotonic fall -> lower barrier. dn=1*1% => -1% => 99 from 100.
h, l, c = bars([100, 99.8, 98.5, 98, 97, 96, 95, 94])
r = run(h, l, c)
assert r["label"][0] == -1, f"fall must label -1, got {r['label'][0]}"
assert r["touch_idx"][0] == 2, f"lower touched at bar 2, got {r['touch_idx'][0]}"
assert r["barrier"][0] == "dn"

# 3. flat -> vertical barrier, label 0, touch at i+VB.
h, l, c = bars([100] * 10)
r = run(h, l, c)
assert r["label"][0] == 0, f"flat must label 0, got {r['label'][0]}"
assert r["touch_idx"][0] == VB, f"vertical at bar {VB}, got {r['touch_idx'][0]}"
assert r["barrier"][0] == "vert"

# 4. INTRABAR: high pierces 102 but every close stays under. A close-only walk
#    misses this and mislabels 0. This is the single most common bug.
h, l, c = bars(closes=[100, 100.2, 100.3, 100.4, 100.5, 100.6],
               highs=[100, 100.3, 102.5, 100.5, 100.6, 100.7],
               lows=[100, 100.1, 100.2, 100.3, 100.4, 100.5])
r = run(h, l, c)
assert r["label"][0] == 1, "must walk intrabar HIGH, not close"
assert r["touch_idx"][0] == 2

# 5. AMBIGUOUS BAR: one bar's range spans both barriers. Order is unknowable
#    from OHLC, so the labeler must assume the LOSS. Assuming the win is how
#    backtests manufacture profit that does not exist.
#    The span is also the FIRST forward bar, which catches np.argmax-on-bool
#    (returns 0 both for "no touch" and "touch at offset 0").
h, l, c = bars(closes=[100] * 7,
               highs=[100, 103.0, 100, 100, 100, 100, 100],
               lows=[100, 98.0, 100, 100, 100, 100, 100])
r = run(h, l, c)
assert r["label"][0] == -1, "ambiguous bar must resolve pessimistically to -1"
assert r["touch_idx"][0] == 1, "touch on the first forward bar must be detected"

# 5b. Unambiguous touch on the first forward bar only (upper).
h, l, c = bars(closes=[100] * 7,
               highs=[100, 102.5, 100, 100, 100, 100, 100],
               lows=[100, 99.5, 100, 100, 100, 100, 100])
r = run(h, l, c)
assert r["label"][0] == 1 and r["touch_idx"][0] == 1, (
    "upper touched at the very first forward bar must label +1, not a timeout")

# 6. Upper at bar 2, lower at bar 4 -> first touch wins.
h, l, c = bars(closes=[100, 100, 102.5, 100, 98.0, 100],
               highs=[100, 100, 102.5, 100, 100.0, 100],
               lows=[100, 100, 100.0, 100, 98.0, 100])
r = run(h, l, c)
assert r["label"][0] == 1 and r["touch_idx"][0] == 2, "first touch wins"

# 7. TRUNCATION: events whose vertical barrier runs past the data end have no
#    known outcome. They must be excluded (label -128 sentinel / NaN), never
#    silently labeled 0 -- that fabricates timeouts at the end of every series.
h, l, c = bars([100] * 8)
r = run(h, l, c)
tail = r["label"][-VB:]
assert all(x == -128 for x in tail), (
    f"last {VB} events lack a resolved outcome and must be excluded, got {tail}")
assert r["label"][0] == 0, "resolvable events still label normally"

# 8. touch_ret sign matches the label, and barrier touches return the barrier
#    level (that is where the order fills), not the bar close.
h, l, c = bars([100, 100.5, 101, 102.5, 103, 104, 105, 106])
r = run(h, l, c)
assert abs(r["touch_ret"][0] - UP * 0.01) < 1e-9, (
    f"upper touch return must be +{UP}*sigma, got {r['touch_ret'][0]}")

# 9. ewma_sigma is causal: sigma[i] must not depend on any bar after i.
c = np.array([100, 101, 99, 103, 97, 105, 95, 110, 90, 120], float)
s_full = ewma_sigma(c, span=5)
s_trunc = ewma_sigma(c[:6], span=5)
assert np.allclose(s_full[:6], s_trunc, equal_nan=True), (
    "ewma_sigma leaks future data -- sigma[i] changed when later bars appeared")

# 10. CONCURRENCY: overlapping events must be down-weighted. Ten events on the
#     same market move are one observation, not ten. Compared WITHIN one call,
#     because normalization forces the mean to 1 and hides cross-call effects.
n = 100
starts = np.array([0] * 10 + [50])          # 10 stacked on bars 0-5, 1 alone
ends = starts + VB
w = sample_weights(np.full(11, 0.01), starts, ends, n)
assert w[10] > w[0] * 5, (
    f"isolated event must far outweigh each of 10 overlapping ones, "
    f"got isolated={w[10]:.4f} vs overlapping={w[0]:.4f}")
assert np.all(w >= 0), "weights must be non-negative"
assert np.all(np.isfinite(w)), "weights must be finite"

# 11. Weights track magnitude: a 5% move outweighs a 1% move, all else equal.
idx = np.array([0, 10])
w = sample_weights(np.array([0.01, 0.05]), idx, idx + VB, n)
assert w[1] > w[0], "larger realized move must carry more weight"

print("all label checks passed")
