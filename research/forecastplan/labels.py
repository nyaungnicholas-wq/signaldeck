import math

WINDOW = 200
HORIZON = 21

def finite(x):
    return not math.isnan(x) and not math.isinf(x)

def max(a, b):
    return a if a > b else b

def roll_mean(x, w):
    n = len(x)
    out = [float('nan')] * n
    if w <= 0:
        return out
    s = 0.0
    for i in range(n):
        s += x[i]
        if i >= w:
            s -= x[i - w]
        if i >= w - 1:
            out[i] = s / w
    return out

def roll_mean_nan(x, w):
    n = len(x)
    out = [float('nan')] * n
    if w <= 0:
        return out
    for i in range(w - 1, n):
        start = i - w + 1
        window = x[start:i + 1]
        all_finite = True
        s = 0.0
        for v in window:
            if not finite(v):
                all_finite = False
                break
            s += v
        if all_finite:
            out[i] = s / w
    return out

def median_of(x):
    finite_vals = [v for v in x if finite(v)]
    if not finite_vals:
        return float('nan')
    finite_vals.sort()
    return finite_vals[len(finite_vals) // 2]

def resolve_trend_at(closes, t, horizon_days):
    n = len(closes)
    if t < 0 or t + horizon_days >= n:
        return None
    sma = roll_mean(closes, WINDOW)
    if sma[t] <= 0 or sma[t + horizon_days] <= 0:
        return None
    d0 = closes[t] / sma[t] - 1
    d1 = closes[t + horizon_days] / sma[t + horizon_days] - 1
    if not finite(d0) or not finite(d1) or d0 == 0 or d1 == 0:
        return None
    act = "downtrend"
    if d1 > 0:
        act = "uptrend"
    return {"actual": act, "key_name": "sma200_distance_pct_at_horizon", "key_value": d1 * 100.0}

def resolve_liquidity_at(closes, volumes, t):
    n = len(closes)
    if len(volumes) != n or t < 0 or t + HORIZON >= n:
        return None
    dv = [0.0] * n
    for i in range(n):
        x = closes[i] * volumes[i]
        if x > 0:
            dv[i] = math.log(x)
        else:
            dv[i] = float('nan')
    m = roll_mean_nan(dv[:t + 1], HORIZON)
    start = max(0, t - WINDOW)
    med = median_of(m[start:t + 1])
    s = 0.0
    cnt = 0
    for x in dv[t + 1:t + 1 + HORIZON]:
        if finite(x):
            s += x
            cnt += 1
    if cnt < HORIZON or not finite(med):
        return None
    fwd = s / cnt
    if fwd == med:
        return None
    act = "quiet"
    if fwd > med:
        act = "active"
    return {"actual": act, "key_name": "fwd21d_mean_log_dollar_vol_minus_trailing_median", "key_value": fwd - med}

def resolve_vol21_at(rets, t):
    n = len(rets)
    if t < 0 or t + HORIZON >= n:
        return None
    hi = t + HORIZON
    rv = [float('nan')] * (hi + 1)
    for i in range(HORIZON, hi + 1):
        sum_ = 0.0
        sq = 0.0
        cnt = 0
        for x in rets[i - HORIZON + 1:i + 1]:
            if finite(x):
                sum_ += x
                sq += x * x
                cnt += 1
        if cnt == HORIZON:
            mean = sum_ / cnt
            arg = sq / cnt - mean * mean
            rv[i] = math.sqrt(arg) if not (arg < 0) else float('nan')  # Go math.Sqrt(neg) is NaN; Python raises
    start = max(0, t - WINDOW)
    med = median_of(rv[start:t + 1])
    fwd = rv[hi]
    if not finite(med) or not finite(fwd) or fwd == med:
        return None
    act = "calm"
    if fwd > med:
        act = "elevated"
    return {"actual": act, "key_name": "fwd21d_realized_vol_minus_trailing_median_daily", "key_value": fwd - med}

def naive_trend_at(closes, t):
    n = len(closes)
    if t < 0 or t >= n:
        return None
    sma = roll_mean(closes, WINDOW)
    if sma[t] <= 0:
        return None
    d = closes[t] / sma[t] - 1
    if not finite(d) or d == 0:
        return None
    if d > 0:
        return "uptrend"
    return "downtrend"

def naive_liquidity_at(closes, volumes, t):
    n = len(closes)
    if len(volumes) != n or t < 0 or t >= n:
        return None
    dv = [0.0] * (t + 1)
    for i in range(t + 1):
        x = closes[i] * volumes[i]
        if x > 0:
            dv[i] = math.log(x)
        else:
            dv[i] = float('nan')
    m = roll_mean_nan(dv, HORIZON)
    cur = m[t]
    start = max(0, t - WINDOW)
    med = median_of(m[start:t + 1])
    if not finite(cur) or not finite(med) or cur == med:
        return None
    if cur > med:
        return "active"
    return "quiet"

def naive_vol21_at(rets, t):
    if t < HORIZON - 1 or t >= len(rets):
        return None
    rv = [float('nan')] * (t + 1)
    for i in range(HORIZON - 1, t + 1):
        sum_ = 0.0
        sq = 0.0
        cnt = 0
        for x in rets[i - HORIZON + 1:i + 1]:
            if finite(x):
                sum_ += x
                sq += x * x
                cnt += 1
        if cnt == HORIZON:
            mean = sum_ / cnt
            arg = sq / cnt - mean * mean
            rv[i] = math.sqrt(arg) if not (arg < 0) else float('nan')  # Go math.Sqrt(neg) is NaN; Python raises
    cur = rv[t]
    start = max(0, t - WINDOW)
    med = median_of(rv[start:t + 1])
    if not finite(cur) or not finite(med) or cur == med:
        return None
    if cur > med:
        return "elevated"
    return "calm"

def bar_returns2(closes):
    if len(closes) < 2:
        return []
    out = []
    for i in range(1, len(closes)):
        try:
            out.append(closes[i] / closes[i - 1] - 1.0)
        except ZeroDivisionError:  # Go float64 division by zero: +-Inf, or NaN for 0/0
            out.append(float('nan') if closes[i] == 0 else math.copysign(math.inf, closes[i]))
    return out

def direction_up(fwd_return):
    return 1 if fwd_return > 0 else 0

def resolve_all(closes, volumes, t, horizon_days):
    returns = bar_returns2(closes)
    trend = resolve_trend_at(closes, t, horizon_days)
    liquidity = resolve_liquidity_at(closes, volumes, t)
    vol21 = resolve_vol21_at(returns, t - 1)
    naive_trend = naive_trend_at(closes, t)
    naive_liquidity = naive_liquidity_at(closes, volumes, t)
    naive_vol21 = naive_vol21_at(returns, t - 1)
    return {
        "trend": trend,
        "liquidity": liquidity,
        "vol21": vol21,
        "naive_trend": naive_trend,
        "naive_liquidity": naive_liquidity,
        "naive_vol21": naive_vol21
    }

def selfcheck():
    # 1
    closes = [100.0] * 300
    volumes = [1000.0] * 300
    t = 250
    assert resolve_trend_at(closes, t, 21) is None
    assert resolve_liquidity_at(closes, volumes, t) is None
    assert resolve_vol21_at(bar_returns2(closes), t - 1) is None
    assert naive_trend_at(closes, t) is None
    assert naive_liquidity_at(closes, volumes, t) is None
    assert naive_vol21_at(bar_returns2(closes), t - 1) is None
    # 2
    closes = [100.0 + 0.1 * i for i in range(400)]
    volumes = [1000.0] * 400
    t = 300
    res = resolve_trend_at(closes, t, 21)
    assert res is not None and res["actual"] == "uptrend" and res["key_value"] > 0
    assert naive_trend_at(closes, t) == "uptrend"
    res2 = resolve_trend_at(closes, t, 63)
    assert res2 is not None and res2["actual"] == "uptrend"
    assert resolve_trend_at(closes, 379, 21) is None
    assert resolve_trend_at(closes, 378, 21) is not None
    # 3
    closes = [100.0 + 0.1 * i for i in range(150)]
    t = 100
    assert resolve_trend_at(closes, t, 21) is None
    # 4a
    closes = [100.0 + 0.1 * i for i in range(400)]
    volumes = [1000.0] * 400
    volumes[310] = 0.0
    t = 300
    assert resolve_liquidity_at(closes, volumes, t) is None
    # 4b
    volumes = [1000.0] * 400
    volumes[100] = 0.0
    assert resolve_liquidity_at(closes, volumes, t) is not None
    # 5
    def lcg(seed):
        while True:
            seed = (1664525 * seed + 1013904223) & 0xffffffff
            yield seed
    gen = lcg(7)
    def rand():
        return next(gen) / 2**32
    n = 600
    rets = []
    for _ in range(n - 1):
        u = rand()
        r = (u - 0.5) * 0.02
        rets.append(r)
    closes = [100.0]
    for r in rets:
        closes.append(closes[-1] * (1.0 + r))
    volumes = []
    for _ in range(n):
        u = rand()
        vol = math.exp((u - 0.5) * 0.5)
        volumes.append(vol)
    t = 400
    assert resolve_trend_at(closes, t, 21) is not None and finite(resolve_trend_at(closes, t, 21)["key_value"])
    assert resolve_liquidity_at(closes, volumes, t) is not None and finite(resolve_liquidity_at(closes, volumes, t)["key_value"])
    assert resolve_vol21_at(bar_returns2(closes), t - 1) is not None and finite(resolve_vol21_at(bar_returns2(closes), t - 1)["key_value"])
    assert naive_trend_at(closes, t) is not None
    assert naive_liquidity_at(closes, volumes, t) is not None
    assert naive_vol21_at(bar_returns2(closes), t - 1) is not None
    all_res = resolve_all(closes, volumes, t, 21)
    assert all_res["trend"] is not None
    assert all_res["liquidity"] is not None
    assert all_res["vol21"] is not None
    assert all_res["naive_trend"] is not None
    assert all_res["naive_liquidity"] is not None
    assert all_res["naive_vol21"] is not None
    # 6
    assert math.isnan(roll_mean([1, 2, 3, 4, 5], 2)[0])
    assert roll_mean([1, 2, 3, 4, 5], 2)[1] == 1.5
    assert roll_mean([1, 2, 3, 4, 5], 2)[2] == 2.5
    assert roll_mean([1, 2, 3, 4, 5], 2)[3] == 3.5
    assert roll_mean([1, 2, 3, 4, 5], 2)[4] == 4.5
    res = roll_mean_nan([1, float('nan'), 3, 4], 2)
    assert math.isnan(res[0]) and math.isnan(res[1]) and math.isnan(res[2]) and res[3] == 3.5
    assert median_of([float('nan'), 3.0, 1.0, 2.0]) == 2.0
    assert median_of([4.0, 1.0, 3.0, 2.0]) == 3.0
    assert math.isnan(median_of([float('nan')]))
    # 7
    assert direction_up(0.0) == 0
    assert direction_up(1e-12) == 1
    assert bar_returns2([1.0, 2.0, 1.0]) == [1.0, -0.5]
    assert bar_returns2([1.0]) == []
    # 8
    closes = [1.0, 2.0, 3.0]
    assert resolve_trend_at(closes, -1, 21) is None
    assert naive_trend_at(closes, -1) is None

if __name__ == "__main__":
    selfcheck()
    print("SELFCHECK OK")