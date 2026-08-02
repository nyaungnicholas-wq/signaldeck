import numpy as np

def ewma_sigma(close: np.ndarray, span: int = 100) -> np.ndarray:
    alpha = 2.0 / (span + 1)
    close = np.asarray(close, dtype=float)  # int input would truncate returns to 0
    r = np.zeros(len(close), dtype=float)
    r[1:] = close[1:] / close[:-1] - 1.0
    sigma = np.zeros(len(close), dtype=float)
    sigma[0] = np.nan
    ewma_var = 0.0
    for i in range(1, len(close)):
        ewma_var = alpha * (r[i] ** 2) + (1 - alpha) * ewma_var
        sigma[i] = np.sqrt(ewma_var)
    sigma[sigma <= 0] = 1e-10
    sigma = np.maximum(sigma, 1e-10)
    return sigma

def _first_true(mask):
    """Index of the first True, or -1 if none.

    np.argmax on a boolean array returns 0 both when nothing is True and when
    element 0 is True. Using it here silently reclassifies every barrier hit on
    the first forward bar as a timeout -- which throws away exactly the fastest,
    strongest moves.
    """
    idx = np.flatnonzero(mask)
    return int(idx[0]) if idx.size else -1


def triple_barrier(high, low, close, sigma, up_mult=2.0, dn_mult=1.0, vbars=20):
    n = len(close)
    label = np.full(n, -128, dtype=np.int8)
    touch_idx = np.full(n, -1, dtype=np.int64)
    touch_ret = np.full(n, np.nan)
    barrier = np.full(n, "none", dtype=object)

    for i in range(n):
        if i + vbars > n - 1:
            continue
        upper = close[i] * (1 + up_mult * sigma[i])
        lower = close[i] * (1 - dn_mult * sigma[i])
        h_slice = high[i+1:i+1+vbars]
        l_slice = low[i+1:i+1+vbars]
        up_touch = _first_true(h_slice >= upper)
        dn_touch = _first_true(l_slice <= lower)
        if up_touch >= 0 and dn_touch >= 0:
            # Equal indices mean one bar's range spanned both barriers. OHLC
            # cannot say which came first, so take the loss. Assuming the win
            # is how a backtest manufactures profit that never existed.
            if up_touch < dn_touch:
                label[i] = 1
                touch_idx[i] = i + 1 + up_touch
                touch_ret[i] = up_mult * sigma[i]
                barrier[i] = "up"
            else:
                label[i] = -1
                touch_idx[i] = i + 1 + dn_touch
                touch_ret[i] = -dn_mult * sigma[i]
                barrier[i] = "dn"
        elif up_touch >= 0:
            label[i] = 1
            touch_idx[i] = i + 1 + up_touch
            touch_ret[i] = up_mult * sigma[i]
            barrier[i] = "up"
        elif dn_touch >= 0:
            label[i] = -1
            touch_idx[i] = i + 1 + dn_touch
            touch_ret[i] = -dn_mult * sigma[i]
            barrier[i] = "dn"
        else:
            label[i] = 0
            touch_idx[i] = i + vbars
            touch_ret[i] = close[i+vbars] / close[i] - 1.0
            barrier[i] = "vert"
    return {"label": label, "touch_idx": touch_idx, "touch_ret": touch_ret, "barrier": barrier}

def sample_weights(touch_ret, event_idx, touch_idx, n_bars):
    concurrency = np.zeros(n_bars, dtype=int)
    for i in range(len(event_idx)):
        start = event_idx[i]
        end = touch_idx[i]
        concurrency[start:end+1] += 1
    uniqueness = np.zeros_like(touch_ret)
    for i in range(len(event_idx)):
        start = event_idx[i]
        end = touch_idx[i]
        conc = concurrency[start:end+1]
        uniqueness[i] = np.mean(1.0 / np.maximum(conc, 1))
    weights = np.abs(touch_ret) * uniqueness
    if np.sum(weights) > 0:
        weights = weights / np.mean(weights)
    else:
        weights = np.ones_like(weights)
    return weights
