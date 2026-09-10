import os, sys, math
import numpy as np
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '..', '..', 'tools'))
import rv_forecast_backtest as T

def har_matrix(rv):
    n = len(rv)
    out = np.full((n, 4), np.nan, dtype=float)
    for i in range(n):
        feat = T.features(rv, i)
        if feat is not None:
            out[i] = feat
    return out

def target_vector(rv, h):
    n = len(rv)
    out = np.full(n, np.nan, dtype=float)
    for t in range(n):
        val = T.target_at(rv, t, h)
        if val is not None:
            out[t] = val
    return out

def design(rv, kind, closes=None, mkt_log_rv=None):
    base = har_matrix(rv)
    if kind == 'har':
        return base
    col = np.full(len(rv), np.nan, dtype=float)
    if kind == 'v1':
        if mkt_log_rv is None:
            raise ValueError("mkt_log_rv required for kind='v1'")
        col = np.asarray(mkt_log_rv, dtype=float)
    elif kind == 'v2':
        if closes is None:
            raise ValueError("closes required for kind='v2'")
        closes = np.asarray(closes, dtype=float)
        for i in range(len(rv)):
            if i == 0 or rv[i] is None or closes[i] is None or closes[i-1] is None or not np.isfinite(rv[i]) or not np.isfinite(closes[i]) or not np.isfinite(closes[i-1]):  # rv carries None holes on real data
                col[i] = np.nan
            else:
                dec = 1.0 if closes[i] < closes[i-1] else 0.0
                col[i] = rv[i] * dec
    elif kind == 'v3':
        for i in range(len(rv)):
            start = max(0, i-62)
            window = rv[start:i+1]
            valid = [x for x in window if x is not None and np.isfinite(x)]
            if len(valid) >= 50:
                col[i] = math.log(np.mean(valid))
            else:
                col[i] = np.nan
    else:
        raise ValueError(f"unknown kind {kind}")
    return np.column_stack((base, col))

def walk(X, y, h, min_history=T.MIN_HISTORY, min_train=T.MIN_TRAIN, refit_every=T.REFIT_EVERY):
    n = len(y)
    forecast = np.full(n, np.nan)
    beta = None
    resid_var = None
    k = X.shape[1] if X.ndim == 2 else 1
    for t in range(min_history, n - h):
        if (t - min_history) % refit_every == 0 or beta is None:
            # build fitting set
            valid_idx = []
            for s in range(T.LAG_M, t - h + 1):
                if not np.isfinite(X[s]).all():
                    continue
                ys = y[s]
                if not np.isfinite(ys) or ys <= 0:
                    continue
                valid_idx.append(s)
            if len(valid_idx) >= min_train:
                Xs = X[valid_idx]
                ys_log = np.log(y[valid_idx])
                try:
                    b, *_ = np.linalg.lstsq(Xs, ys_log, rcond=None)
                    residuals = ys_log - Xs @ b
                    var = np.sum(residuals**2) / (len(valid_idx) - k)
                    beta = b
                    resid_var = var
                except np.linalg.LinAlgError:
                    pass  # keep previous fit
        if beta is not None and np.isfinite(X[t]).all():
            forecast[t] = np.exp(X[t] @ beta + resid_var/2)
    return forecast

def selfcheck():
    rng = np.random.default_rng(7)
    series_count = 3
    checks = 0
    for _ in range(series_count):
        n = 800
        log_price = np.cumsum(rng.normal(0, 0.02, n))
        close = np.exp(log_price)
        open_price = np.empty_like(close)
        high = np.empty_like(close)
        low = np.empty_like(close)
        ts = np.empty(n, dtype=int)
        for i in range(n):
            ts[i] = 1_600_000_000 + i * 86400
            N = rng.normal()
            if i == 0:
                open_price[i] = close[i]
            else:
                open_price[i] = close[i-1] * math.exp(N * 0.005)
            high[i] = close[i] * math.exp(abs(N) * 0.01)
            low[i] = close[i] * math.exp(-abs(N) * 0.01)
        bars = list(zip(ts, open_price, high, low, close))
        rv_list = T.rv_series(bars)[1]
        # har_matrix check
        for i in range(len(rv_list)):
            feat = T.features(rv_list, i)
            row = har_matrix(rv_list)[i]
            if feat is None:
                if not np.all(np.isnan(row)):
                    raise AssertionError("har_matrix mismatch None")
            else:
                if not np.allclose(row, feat, rtol=1e-12, atol=1e-12):
                    raise AssertionError("har_matrix mismatch value")
        checks += 1
        # target_vector check for h=1,5
        for h in (1,5):
            tv = target_vector(rv_list, h)
            for t in range(len(rv_list)):
                val = T.target_at(rv_list, t, h)
                if val is None:
                    if not np.isnan(tv[t]):
                        raise AssertionError("target_vector mismatch None")
                else:
                    if not np.isclose(tv[t], val, rtol=1e-12, atol=1e-12):
                        raise AssertionError("target_vector mismatch value")
            checks += 1
        # walk check
        for h in (1,5):
            X = har_matrix(rv_list)
            y = target_vector(rv_list, h)
            our = walk(X, y, h)
            records = T.walk_symbol(ts, rv_list, h)
            # Build dict from records
            rec_dict = {list(ts).index(rec["ts"]): rec["har"] for rec in records}  # tool records are dicts keyed by call-bar ts
            for t in range(len(rv_list)):
                if t in rec_dict:
                    rec_val = rec_dict[t]
                    our_val = our[t]
                    if np.isnan(rec_val):
                        if not np.isnan(our_val):
                            raise AssertionError("walk mismatch NaN vs value")
                    else:
                        if np.isnan(our_val) or not np.isclose(our_val, rec_val, rtol=1e-8, atol=1e-12):
                            raise AssertionError("walk mismatch value")
                else:
                    if not np.isnan(our[t]):
                        raise AssertionError("walk extra value")
            checks += 1
        # design shape checks
        closes = np.asarray([c for (_,_,_,_,c) in bars], dtype=float)
        if design(rv_list, 'v3').shape[1] != 5:
            raise AssertionError("design v3 shape")
        if design(rv_list, 'v2', closes=closes).shape[1] != 5:
            raise AssertionError("design v2 shape")
        checks += 1
    print(f"PARITY OK checks={checks}")
    print("SELFCHECK OK")

if __name__ == "__main__":
    selfcheck()