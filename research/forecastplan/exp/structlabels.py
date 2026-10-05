"""Whole-series structural labels, faithful to labels.py (the Go resolver port).

labels.py answers one t per call and rebuilds its rolling series each time, so
labelling a 2,000-bar series costs O(n^2 * H). These functions compute the shared
series once and apply the SAME rules at every t. The selfcheck compares every t
of four constructed series against the reference functions, None for None.

Two deliberate quirks of the reference are preserved rather than "fixed":
  * roll_mean is a running sum, so one NaN close poisons every later SMA value;
  * resolve_vol21_at starts its realised-vol array at index HORIZON while
    naive_vol21_at starts at HORIZON-1, so the two see different windows at the
    very first index. Matching the grader beats tidiness.
"""
import math
import os
import sys

import numpy as np

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
import labels  # noqa: E402

WINDOW = labels.WINDOW
HORIZON = labels.HORIZON
finite = labels.finite


def _obj(n):
    return np.array([None] * n, dtype=object)


def _floats(x):
    return [float(v) for v in x]


def trend_actual(closes, horizon_days):
    closes = _floats(closes)
    n = len(closes)
    out = _obj(n)
    sma = labels.roll_mean(closes, WINDOW)
    for t in range(n):
        if t + horizon_days >= n:
            break
        if sma[t] <= 0 or sma[t + horizon_days] <= 0:
            continue
        d0 = closes[t] / sma[t] - 1
        d1 = closes[t + horizon_days] / sma[t + horizon_days] - 1
        if not finite(d0) or not finite(d1) or d0 == 0 or d1 == 0:
            continue
        out[t] = "uptrend" if d1 > 0 else "downtrend"
    return out


def naive_trend(closes):
    closes = _floats(closes)
    n = len(closes)
    out = _obj(n)
    sma = labels.roll_mean(closes, WINDOW)
    for t in range(n):
        if sma[t] <= 0:
            continue
        d = closes[t] / sma[t] - 1
        if not finite(d) or d == 0:
            continue
        out[t] = "uptrend" if d > 0 else "downtrend"
    return out


def _log_dollar_volume(closes, volumes):
    dv = []
    for c, v in zip(closes, volumes):
        x = c * v
        dv.append(math.log(x) if x > 0 else float("nan"))
    return dv


def liquidity_actual(closes, volumes):
    closes, volumes = _floats(closes), _floats(volumes)
    n = len(closes)
    out = _obj(n)
    if len(volumes) != n:
        return out
    dv = _log_dollar_volume(closes, volumes)
    # The reference computes roll_mean_nan over dv[:t+1]; a trailing window mean
    # at positions <= t is identical when computed once over the whole series.
    m = labels.roll_mean_nan(dv, HORIZON)
    for t in range(n):
        if t + HORIZON >= n:
            break
        med = labels.median_of(m[max(0, t - WINDOW):t + 1])
        window = dv[t + 1:t + 1 + HORIZON]
        if not all(finite(x) for x in window) or len(window) < HORIZON or not finite(med):
            continue
        acc = 0.0  # naive left-to-right accumulation like the reference; Python 3.12 sum() is compensated and can differ by an ulp
        for x in window:
            acc += x
        fwd = acc / HORIZON
        if fwd == med:
            continue
        out[t] = "active" if fwd > med else "quiet"
    return out


def naive_liquidity(closes, volumes):
    closes, volumes = _floats(closes), _floats(volumes)
    n = len(closes)
    out = _obj(n)
    if len(volumes) != n:
        return out
    m = labels.roll_mean_nan(_log_dollar_volume(closes, volumes), HORIZON)
    for t in range(n):
        cur = m[t]
        med = labels.median_of(m[max(0, t - WINDOW):t + 1])
        if not finite(cur) or not finite(med) or cur == med:
            continue
        out[t] = "active" if cur > med else "quiet"
    return out


def _realized_vol(rets, first):
    """21-return realised vol at every index >= first (NaN below), reference formula."""
    n = len(rets)
    rv = [float("nan")] * n
    for i in range(first, n):
        window = rets[i - HORIZON + 1:i + 1]
        if len(window) < HORIZON or not all(finite(x) for x in window):
            continue
        s, sq = 0.0, 0.0
        for x in window:  # naive accumulation, matching the reference bit for bit
            s += x
            sq += x * x
        mean = s / HORIZON
        arg = sq / HORIZON - mean * mean
        rv[i] = math.sqrt(arg) if not (arg < 0) else float("nan")
    return rv


def vol21_actual(rets):
    rets = _floats(rets)
    n = len(rets)
    out = _obj(n)
    rv = _realized_vol(rets, HORIZON)  # the resolver's array starts at HORIZON
    for t in range(n):
        hi = t + HORIZON
        if hi >= n:
            break
        med = labels.median_of(rv[max(0, t - WINDOW):t + 1])
        fwd = rv[hi]
        if not finite(med) or not finite(fwd) or fwd == med:
            continue
        out[t] = "elevated" if fwd > med else "calm"
    return out


def naive_vol21(rets):
    rets = _floats(rets)
    n = len(rets)
    out = _obj(n)
    rv = _realized_vol(rets, HORIZON - 1)  # the null's array starts one index earlier
    for t in range(HORIZON - 1, n):
        cur = rv[t]
        med = labels.median_of(rv[max(0, t - WINDOW):t + 1])
        if not finite(cur) or not finite(med) or cur == med:
            continue
        out[t] = "elevated" if cur > med else "calm"
    return out


def bar_returns2(closes):
    return np.array(labels.bar_returns2(_floats(closes)), dtype=float)


def _series():
    rng = np.random.default_rng(20260909)
    a_close = list(100.0 * np.cumprod(1.0 + rng.normal(0, 0.01, 500)))
    a_vol = list(np.exp(rng.normal(0, 0.5, 500)))
    b_close, b_vol = list(a_close), list(a_vol)
    b_vol[310] = 0.0
    b_close[120] = float("nan")
    c_close, c_vol = [100.0] * 300, [1000.0] * 300
    d_close = [100.0 + 0.1 * i for i in range(400)]
    d_vol = list(np.exp(rng.normal(0, 0.3, 400)))
    return [("walk", a_close, a_vol), ("holes", b_close, b_vol),
            ("flat", c_close, c_vol), ("rising", d_close, d_vol)]


def selfcheck():
    checks, mismatches = 0, []
    for name, closes, vols in _series():
        n = len(closes)
        got = {h: trend_actual(closes, h) for h in (21, 63)}
        nt, la, nl = naive_trend(closes), liquidity_actual(closes, vols), naive_liquidity(closes, vols)
        for t in range(n):
            for h in (21, 63):
                ref = labels.resolve_trend_at(closes, t, h)
                exp = ref["actual"] if ref else None
                checks += 1
                if got[h][t] != exp:
                    mismatches.append((name, "trend", h, t, got[h][t], exp))
            exp = labels.naive_trend_at(closes, t)
            checks += 1
            if nt[t] != exp:
                mismatches.append((name, "naive_trend", t, nt[t], exp))
            ref = labels.resolve_liquidity_at(closes, vols, t)
            exp = ref["actual"] if ref else None
            checks += 1
            if la[t] != exp:
                mismatches.append((name, "liquidity", t, la[t], exp))
            exp = labels.naive_liquidity_at(closes, vols, t)
            checks += 1
            if nl[t] != exp:
                mismatches.append((name, "naive_liquidity", t, nl[t], exp))
        rets = labels.bar_returns2(closes)
        va, nv = vol21_actual(rets), naive_vol21(rets)
        for t in range(len(rets)):
            ref = labels.resolve_vol21_at(rets, t)
            exp = ref["actual"] if ref else None
            checks += 1
            if va[t] != exp:
                mismatches.append((name, "vol21", t, va[t], exp))
            exp = labels.naive_vol21_at(rets, t)
            checks += 1
            if nv[t] != exp:
                mismatches.append((name, "naive_vol21", t, nv[t], exp))
        mine = bar_returns2(closes)
        assert len(mine) == len(rets)
        for x, y in zip(mine, rets):
            assert (math.isnan(x) and math.isnan(y)) or x == y or abs(x - y) < 1e-12, (x, y)
    assert not mismatches, mismatches[:10]
    print(f"PARITY OK comparisons={checks}")
    print("SELFCHECK OK")


if __name__ == "__main__":
    selfcheck()
