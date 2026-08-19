"""The three IC estimators must agree on cases whose answer is known.

An inference method that never rejects is not conservative, it is broken -- and it
would have produced this investigation's UNRESOLVED verdict for free. So test both
directions: a real effect must be FOUND, and pure noise must NOT be.
"""
import numpy as np
import pandas as pd
import ic_inference as m

days = pd.date_range("2020-01-01", periods=1000, freq="D")
rng = np.random.default_rng(7)


def series(vals):
    return pd.Series(vals, index=days)


# --- 1. a large, obvious effect must be detected by all three -------------------
strong = series(rng.normal(0.30, 0.10, len(days)))
a = m.method_a_blocks(strong, 21)
b = m.method_b_moving_block(strong, 21)
c = m.method_c_hac(strong, 20)
assert a["t"] > 5, f"method A missed an obvious effect: t={a['t']}"
assert b["lo"] > 0, f"method B missed an obvious effect: {b}"
assert c["t"] > 5, f"method C missed an obvious effect: t={c['t']}"

# --- 2. pure zero-mean noise must NOT be called significant ---------------------
noise = series(rng.normal(0.0, 0.13, len(days)))
a0 = m.method_a_blocks(noise, 21)
b0 = m.method_b_moving_block(noise, 21)
c0 = m.method_c_hac(noise, 20)
assert abs(a0["t"]) < 2, f"method A found signal in noise: t={a0['t']}"
assert b0["lo"] <= 0 <= b0["hi"], f"method B found signal in noise: {b0}"
assert abs(c0["t"]) < 2, f"method C found signal in noise: t={c0['t']}"

# --- 3. HAC must WIDEN the interval when the series is autocorrelated -----------
# This is the whole point of the correction: overlapping labels induce dependence,
# and an estimator that ignores it reports a spuriously narrow interval.
raw = rng.normal(0.02, 0.13, len(days))
ar = np.copy(raw)
for i in range(1, len(ar)):
    ar[i] = 0.9 * ar[i - 1] + 0.1 * raw[i]
naive_se = ar.std(ddof=1) / np.sqrt(len(ar))
hac_se = m.method_c_hac(series(ar), 20)["se"]
assert hac_se > naive_se * 1.5, (
    f"HAC did not widen on a strongly autocorrelated series: "
    f"hac={hac_se:.5f} naive={naive_se:.5f} -- the correction is not working")

# --- 4. determinism ------------------------------------------------------------
assert m.method_b_moving_block(noise, 21) == m.method_b_moving_block(noise, 21), \
    "bootstrap is not seeded"

print("OK  all three estimators detect a real effect, reject noise, and HAC widens "
      f"under autocorrelation ({hac_se/naive_se:.1f}x)")
