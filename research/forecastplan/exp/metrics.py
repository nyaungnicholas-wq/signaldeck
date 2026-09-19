import numpy as np

def accuracy(p, y, thr=0.5) -> float:
    return np.mean((p >= thr) == (y == 1))

def balanced_accuracy(p, y, thr=0.5) -> float | None:
    tp = np.sum((p >= thr) & (y == 1))
    tn = np.sum((p < thr) & (y == 0))
    fp = np.sum((p >= thr) & (y == 0))
    fn = np.sum((p < thr) & (y == 1))
    tpr = tp / (tp + fn) if (tp + fn) > 0 else None
    tnr = tn / (tn + fp) if (tn + fp) > 0 else None
    if tpr is None or tnr is None:
        return None
    return 0.5 * (tpr + tnr)

def brier(p, y) -> float:
    return np.mean((p - y) ** 2)

def log_loss(p, y, eps=1e-6) -> float:
    pp = np.clip(p, eps, 1 - eps)
    return -np.mean(y * np.log(pp) + (1 - y) * np.log(1 - pp))

def ece(p, y, bins=10) -> float:
    edges = np.linspace(0, 1, bins + 1)
    e = 0.0
    n = len(p)
    for i in range(bins):
        low, high = edges[i], edges[i + 1]
        if i == bins - 1:
            mask = (p >= low) & (p <= high)
        else:
            mask = (p >= low) & (p < high)
        w = np.sum(mask) / n
        if w > 0:
            mean_p = np.mean(p[mask])
            realized = np.mean(y[mask])
            e += w * np.abs(mean_p - realized)
    return e

def _rankdata(x):
    order = np.argsort(x, kind='mergesort')
    sorted_x = x[order]
    ranks = np.empty_like(x, dtype=float)
    i = 0
    n = len(sorted_x)
    while i < n:
        j = i
        while j < n and sorted_x[j] == sorted_x[i]:
            j += 1
        avg_rank = (i + 1 + j) / 2.0
        ranks[order[i:j]] = avg_rank
        i = j
    return ranks

def auc(p, y) -> float | None:
    pos = y == 1
    neg = y == 0
    n_pos = np.sum(pos)
    n_neg = np.sum(neg)
    if n_pos == 0 or n_neg == 0:
        return None
    ranks = _rankdata(p)
    sum_ranks_pos = np.sum(ranks[pos])
    auc_val = (sum_ranks_pos - n_pos * (n_pos + 1) / 2) / (n_pos * n_neg)
    return auc_val

def within_day_auc(p, y, day, min_n=30) -> tuple[float | None, float | None, int]:
    uniq_days = np.unique(day)
    aucs = []
    for d in uniq_days:
        mask = day == d
        n = np.sum(mask)
        if n >= min_n:
            y_sub = y[mask]
            if np.sum(y_sub) > 0 and np.sum(y_sub) < n:
                a = auc(p[mask], y_sub)
                if a is not None:
                    aucs.append(a)
    k = len(aucs)
    if k == 0:
        return (None, None, 0)
    if k == 1:
        return (aucs[0], None, 1)
    mean_val = np.mean(aucs)
    se = np.std(aucs, ddof=1) / np.sqrt(k)
    return (mean_val, se, k)

def prequential_majority(day, y) -> np.ndarray:
    uniq_days = np.unique(day)
    out = np.full_like(day, np.nan, dtype=float)
    cum_ups = 0
    cum_total = 0
    for d in uniq_days:
        mask = day == d
        if cum_total > 0:
            if cum_ups * 2 > cum_total:
                val = 1.0
            elif cum_ups * 2 < cum_total:
                val = 0.0
            else:
                val = np.nan
        else:
            val = np.nan
        out[mask] = val
        cum_ups += np.sum(y[mask])
        cum_total += np.sum(mask)
    return out

def hits(p, y, thr=0.5) -> np.ndarray:
    return ((p >= thr) == (y == 1)).astype(float)

def block_bootstrap(stat_rows, day, block_len=21, n_boot=2000, seed=20260909) -> dict:
    mask = ~np.isnan(stat_rows)
    stat_rows = stat_rows[mask]
    day = day[mask]
    uniq_days = np.unique(day)
    day_sums = []
    day_counts = []
    for d in uniq_days:
        dm = day == d
        day_sums.append(np.sum(stat_rows[dm]))
        day_counts.append(np.sum(dm))
    day_sums = np.array(day_sums)
    day_counts = np.array(day_counts)
    n_days = len(uniq_days)
    block_means = []
    for start in range(0, n_days, block_len):
        end = min(start + block_len, n_days)
        s = day_sums[start:end].sum()
        c = day_counts[start:end].sum()
        block_means.append(s / c if c > 0 else np.nan)
    block_means = np.array(block_means)
    n_blocks = len(block_means)
    point = np.mean(block_means) if n_blocks > 0 else np.nan
    if n_blocks < 2:
        return {
            'point': point,
            'lo': None,
            'hi': None,
            'p_le_0': None,
            'n_blocks': n_blocks,
            'n_rows': len(stat_rows),
            'block_len': block_len
        }
    rng = np.random.default_rng(seed)
    idx = rng.integers(0, n_blocks, size=(n_boot, n_blocks))
    boot_stats = block_means[idx].mean(axis=1)
    lo = np.percentile(boot_stats, 2.5)
    hi = np.percentile(boot_stats, 97.5)
    p_le_0 = np.mean(boot_stats <= 0)
    return {
        'point': point,
        'lo': lo,
        'hi': hi,
        'p_le_0': p_le_0,
        'n_blocks': n_blocks,
        'n_rows': len(stat_rows),
        'block_len': block_len
    }

def holm(pvals: dict) -> dict:
    items = [(k, v) for k, v in pvals.items() if v is not None]
    m = len(items)
    if m == 0:
        return {}
    sorted_items = sorted(items, key=lambda x: x[1])
    p_sorted = np.array([v for _, v in sorted_items])
    ranks = np.arange(1, m + 1)
    adj_raw = p_sorted * (m - ranks + 1)
    adj = np.maximum.accumulate(adj_raw)
    adj = np.minimum(adj, 1.0)
    reject = adj <= 0.05
    result = {}
    for (name, _), adj_val, rej in zip(sorted_items, adj, reject):
        result[name] = {'p': _, 'p_adj': float(adj_val), 'reject': bool(rej)}
    return result

def reliability(p, y, bins=10) -> list:
    edges = np.linspace(0, 1, bins + 1)
    out = []
    for i in range(bins):
        low, high = edges[i], edges[i + 1]
        if i == bins - 1:
            mask = (p >= low) & (p <= high)
        else:
            mask = (p >= low) & (p < high)
        n = np.sum(mask)
        if n > 0:
            mean_p = np.mean(p[mask])
            realized = np.mean(y[mask])
            out.append([float(mean_p), float(realized), int(n)])
        else:
            out.append([None, None, 0])
    return out

def selfcheck():
    # auc
    assert auc(np.array([0.9,0.8,0.2,0.1]), np.array([1,1,0,0])) == 1.0
    assert auc(np.array([0.5]*4), np.array([1,1,0,0])) == 0.5
    # accuracy
    assert accuracy(np.array([0.6,0.4,0.7]), np.array([1,0,1]), thr=0.5) == 1.0
    # brier
    assert brier(np.array([1,0]), np.array([1,0])) == 0.0
    # log_loss
    ll = log_loss(np.array([0.5,0.5]), np.array([0,1]))
    assert abs(ll - np.log(2)) < 1e-12
    # ece - calibrated fixture: two bins, perfect calibration
    p = np.array([0.0,0.0,1.0,1.0])
    y = np.array([0,0,1,1])
    e = ece(p, y, bins=2)
    assert e < 1e-12  # near zero
    # holm
    res = holm({'a':0.01,'b':0.02,'c':0.03})
    assert res['a']['p_adj'] == 0.03
    assert res['b']['p_adj'] == 0.04
    assert res['c']['p_adj'] == 0.04
    assert all(v['reject'] for v in res.values())
    # prequential_majority
    day = np.array([1,1,2,2,3,3])
    y = np.array([1,1,0,0,1,0])
    pm = prequential_majority(day, y)
    expected = np.array([np.nan,np.nan,1,1,np.nan,np.nan])
    assert np.allclose(pm, expected, equal_nan=True)
    # block_bootstrap
    rng = np.random.default_rng(0)
    days = np.repeat(np.arange(20), 10)  # 200 rows, 20 days
    stat = 1.0 + rng.normal(0, 0.01, size=days.shape)
    res = block_bootstrap(stat, days, block_len=5, n_boot=500, seed=42)
    assert res['p_le_0'] == 0.0
    assert res['lo'] > 0
    # within_day_auc
    p = np.array([0.9,0.8,0.1,0.2,0.9,0.8])
    y = np.array([1,1,0,0,1,1])
    day = np.array([1,1,1,2,2,2])
    mean_auc, se, k = within_day_auc(p, y, day, min_n=2)
    assert k == 2
    assert mean_auc == 1.0
    print("SELFCHECK OK")

if __name__ == "__main__":
    selfcheck()