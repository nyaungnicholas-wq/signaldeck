import numpy as np

def outer_splits(days, horizon, block_len=126, n_blocks=6, embargo=21, last_origin=None):
    if last_origin is None:
        last_origin = days[-1]
    lo_idx = np.searchsorted(days, last_origin, side='right') - 1
    if days[lo_idx] != last_origin:
        raise ValueError("last_origin not found in days")
    required = n_blocks * block_len + horizon + embargo + 252
    if len(days) < required:
        raise ValueError("calendar too short for requested splits")
    splits = []
    for k in range(1, n_blocks + 1):
        offset = (n_blocks - k) * block_len
        te_idx = lo_idx - offset
        ts_idx = te_idx - block_len + 1
        if ts_idx < 0:
            raise ValueError("block start before calendar begin")
        test_start = days[ts_idx]
        test_end = days[te_idx]
        train_end_idx = ts_idx - 1 - horizon - embargo
        if train_end_idx < 0:
            raise ValueError("train window too short")
        train_start = days[0]
        train_end = days[train_end_idx]
        splits.append({
            'block': k,
            'test_start': test_start,
            'test_end': test_end,
            'train_start': train_start,
            'train_end': train_end,
            'test_pos': (ts_idx, te_idx),
            'train_pos': (0, train_end_idx)
        })
    return splits

def inner_folds(days, train_end, horizon, fold_len=126, n_folds=3, embargo=21):
    te_idx = np.searchsorted(days, train_end, side='right') - 1
    if days[te_idx] != train_end:
        raise ValueError("train_end not found in days")
    window_len = te_idx + 1
    required = n_folds * fold_len + horizon + embargo + 252
    if window_len < required:
        raise ValueError("training window too short for requested folds")
    folds = []
    for k in range(1, n_folds + 1):
        offset = (n_folds - k) * fold_len
        ve_idx = te_idx - offset
        vs_idx = ve_idx - fold_len + 1
        if vs_idx < 0:
            raise ValueError("fold start before training window begin")
        val_start = days[vs_idx]
        val_end = days[ve_idx]
        train_end_idx = vs_idx - 1 - horizon - embargo
        if train_end_idx < 0:
            raise ValueError("fold train window too short")
        train_start = days[0]
        train_end = days[train_end_idx]
        folds.append({
            'fold': k,
            'val_start': val_start,
            'val_end': val_end,
            'train_start': train_start,
            'train_end': train_end,
            'val_pos': (vs_idx, ve_idx),
            'train_pos': (0, train_end_idx)
        })
    return folds

def mask_between(day_array, start, end):
    return (day_array >= start) & (day_array <= end)

def describe(splits):
    lines = []
    for s in splits:
        if 'test_start' in s:
            lines.append(f"Block {s['block']}: train {s['train_start']} to {s['train_end']}, test {s['test_start']} to {s['test_end']}")
        else:
            lines.append(f"Fold {s['fold']}: train {s['train_start']} to {s['train_end']}, val {s['val_start']} to {s['val_end']}")
    return lines

def selfcheck():
    # integer calendar
    days_int = np.arange(2000)
    s = outer_splits(days_int, horizon=5)
    assert len(s) == 6
    assert s[-1]['test_end'] == 1999
    expected_first_start = 1999 - 6 * 126 + 1
    assert s[0]['test_start'] == expected_first_start
    for block in s:
        mask = mask_between(days_int, block['test_start'], block['test_end'])
        assert mask.sum() == 126
        assert block['train_end'] == block['test_start'] - 1 - 5 - 21
    # adjacency
    for i in range(len(s) - 1):
        assert s[i]['test_end'] + 1 == s[i + 1]['test_start']
    f = inner_folds(days_int, train_end=s[0]['train_end'], horizon=5)
    assert len(f) == 3
    assert f[-1]['val_end'] == s[0]['train_end']
    for fold in f:
        mask = mask_between(days_int, fold['val_start'], fold['val_end'])
        assert mask.sum() == 126
        assert fold['train_end'] == fold['val_start'] - 1 - 5 - 21
    assert f[0]['val_start'] > 252
    # datetime64 calendar
    days_dt = np.arange('2018-01-01', '2026-01-01', dtype='datetime64[D]')
    s_dt = outer_splits(days_dt, horizon=5)
    assert len(s_dt) == 6
    assert s_dt[-1]['test_end'] == days_dt[-1]
    expected_first_start_dt = days_dt[-1] - np.timedelta64(6 * 126, 'D') + np.timedelta64(1, 'D')
    assert s_dt[0]['test_start'] == expected_first_start_dt
    for block in s_dt:
        mask = mask_between(days_dt, block['test_start'], block['test_end'])
        assert mask.sum() == 126
        assert block['train_end'] == block['test_start'] - np.timedelta64(5 + 21, 'D') - np.timedelta64(1, 'D')
    f_dt = inner_folds(days_dt, train_end=s_dt[0]['train_end'], horizon=5)
    assert len(f_dt) == 3
    assert f_dt[-1]['val_end'] == s_dt[0]['train_end']
    for fold in f_dt:
        mask = mask_between(days_dt, fold['val_start'], fold['val_end'])
        assert mask.sum() == 126
        assert fold['train_end'] == fold['val_start'] - np.timedelta64(5 + 21, 'D') - np.timedelta64(1, 'D')
    assert f_dt[0]['val_start'] > np.datetime64('2018-01-01') + np.timedelta64(252, 'D')
    # mask_between counts match already checked in loops
    # ValueError on short calendar
    days_short = np.arange(300)
    try:
        outer_splits(days_short, horizon=5)
        assert False, "Expected ValueError"
    except ValueError:
        pass
    print("SELFCHECK OK")

if __name__ == "__main__":
    selfcheck()