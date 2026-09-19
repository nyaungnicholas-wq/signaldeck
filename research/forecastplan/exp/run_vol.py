import os, sys, json, math, argparse, tempfile, numpy as np
THIS = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(THIS, '..', '..', '..'))
sys.path.insert(0, os.path.join(REPO, 'tools'))
sys.path.insert(0, THIS)
import rv_forecast_backtest as T
import vol_lib as V
import metrics as M

def load_universe(con, limit, seed):
    syms = T.universe(con)
    if limit > 0:
        rng = np.random.default_rng(seed)
        syms = sorted(rng.choice(syms, size=min(limit, len(syms)), replace=False).tolist())
    out = {}
    for sid in syms:
        bars = T.bars_for(con, sid)
        if len(bars) < 530:
            continue
        ts, rv, _ = T.rv_series(bars)
        out[sid] = {'ts': ts, 'rv': rv, 'closes': [b[4] for b in bars]}
    return out

def market_log_rv(data, min_symbols=30):
    all_ts = set()
    for v in data.values():
        all_ts.update(v['ts'])
    mkt = {}
    for t in all_ts:
        vals = []
        for v in data.values():
            rv = v['rv'][v['ts'].index(t)] if t in v['ts'] else None
            if rv is not None and rv > 0:
                vals.append(math.log(rv))
        if len(vals) >= min_symbols:
            mkt[t] = float(np.mean(vals))
    return mkt

def evaluate_horizon(data, mkt, h):
    per_day = {}
    rows = 0
    used_sids = 0
    for sid, v in data.items():
        ts, rv, closes = v['ts'], v['rv'], v['closes']
        if len(rv) < 530:
            continue
        used_sids += 1
        mk = np.array([mkt.get(t, np.nan) for t in ts])
        X = {
            'har': V.har_matrix(rv),
            'v1': V.design(rv, 'v1', mkt_log_rv=mk),
            'v2': V.design(rv, 'v2', closes=closes),
            'v3': V.design(rv, 'v3')
        }
        y = V.target_vector(rv, h)
        F = {k: V.walk(X[k], y, h) for k in X}
        ew = T.ewma_series(rv)
        for i in range(len(rv)):
            a = y[i]
            if not np.isfinite(a) or a <= 0:
                continue
            cand = {k: F[k][i] for k in F}
            cand['ewma'] = ew[i]
            cand['rw'] = rv[i]
            if any(v is None or not np.isfinite(v) or v <= 0 for v in cand.values()):
                continue
            d = T.day_key(ts[i])
            for k, f in cand.items():
                per_day.setdefault(d, {}).setdefault(k, []).append(T.qlike(a, f))
            rows += 1
    if not per_day:
        return {'horizon': h, 'rows': 0, 'symbols': used_sids, 'n_days': 0, 'hac_lag': 0,
                'mean_qlike': {}, 'comparisons': {}, 'by_year': {}}
    days = sorted(per_day)
    daily = {k: [float(np.mean(per_day[d][k])) for d in days] for k in ('har','v1','v2','v3','ewma','rw')}
    lag = max(T.newey_west_lag(len(days)), h)
    comparisons = {}
    mean_har = float(np.mean(daily['har']))
    for k in ('v1','v2','v3','ewma','rw'):
        d = [daily[k][i] - daily['har'][i] for i in range(len(days))]
        dm = T.diebold_mariano(d, lag)
        t = dm['t'] if dm else None
        p = math.erfc(abs(t)/math.sqrt(2)) if t is not None else None
        ci = T.block_bootstrap_ci(d)
        comparisons[k] = {
            'mean_diff': float(np.mean(d)),
            'dm_t': t,
            'p': p,
            'ci': ci,
            'rel_improvement_vs_har': (mean_har - float(np.mean(daily[k]))) / mean_har if mean_har != 0 else None,
            'n_days': len(days)
        }
    by_year = {}
    from collections import defaultdict
    year_map = defaultdict(list)
    for d in days:
        year = d[:4]
        year_map[year].append(d)
    for year, ds in year_map.items():
        idx = [days.index(d) for d in ds]
        by_year[year] = {}
        for k in ('v1','v2','v3'):
            diffs = [daily[k][i] - daily['har'][i] for i in idx]
            by_year[year][k] = float(np.mean(diffs)) if diffs else 0.0
    return {
        'horizon': h,
        'rows': rows,
        'symbols': used_sids,
        'n_days': len(days),
        'hac_lag': lag,
        'mean_qlike': {k: float(np.mean(daily[k])) for k in daily},
        'comparisons': comparisons,
        'by_year': by_year
    }

def write_outputs(results, holm, meta, out_dir):
    os.makedirs(out_dir, exist_ok=True)
    with open(os.path.join(out_dir, 'vol_results.json'), 'w') as f:
        json.dump({'results': results, 'holm': holm, 'meta': meta}, f, indent=1, default=str)
    md_lines = ['horizon|model|mean QLIKE|diff vs HAR|DM t|p|Holm p_adj|CI|rel improvement|n_days',
                '---|---|---|---|---|---|---|---|---|---']
    for h, res in results.items():
        for model in ('har','v1','v2','v3','ewma','rw'):
            if model == 'har':
                continue
            comp = res['comparisons'].get(model)
            if comp is None:
                continue
            holm_entry = holm.get(f'{model}@h{h}', {'p_adj': None})
            md_lines.append(f"{h}|{model}|{res['mean_qlike'][model]:.6f}|{comp['mean_diff']:.6f}|{comp['dm_t'] if comp['dm_t'] is not None else ''}|{comp['p'] if comp['p'] is not None else ''}|{holm_entry['p_adj'] if holm_entry['p_adj'] is not None else ''}|{str(comp['ci'])}|{comp['rel_improvement_vs_har'] if comp['rel_improvement_vs_har'] is not None else ''}|{comp['n_days']}")
    with open(os.path.join(out_dir, 'vol_summary.md'), 'w') as f:
        f.write('\n'.join(md_lines))
    ledger_path = os.path.join(out_dir, 'ledger.jsonl')
    with open(ledger_path, 'a') as f:
        for h, res in results.items():
            for model in ('har','v1','v2','v3','ewma','rw'):
                if model == 'har':
                    continue
                comp = res['comparisons'].get(model)
                if comp is None:
                    continue
                rec = {
                    'trial_id': f'vol-h{h}-{model}',
                    'family': 'volatility',
                    'kind_or_horizon': h,
                    'model': model,
                    'params': {'refit_every': 5, 'min_train': 500, 'min_history': 530},
                    'feature_set': model,
                    'calibration': 'none',
                    'stage': 'outer',
                    'block_or_fold': 'walk-forward',
                    'n_train': None,
                    'n_test': res['rows'],
                    'metrics': comp,
                    'duration_s': 0,
                    'started_utc': '',
                    'status': 'ok',
                    'artifacts': ['vol_results.json'],
                    'command': ' '.join(sys.argv)
                }
                f.write(json.dumps(rec) + '\n')

def main():
    p = argparse.ArgumentParser()
    p.add_argument('--db', default='data/signaldeck.db')
    p.add_argument('--out-dir', default='research/forecastplan/exp/out')
    p.add_argument('--horizons', default='1,5')
    p.add_argument('--limit-symbols', type=int, default=0)
    p.add_argument('--seed', type=int, default=20260909)
    p.add_argument('--selfcheck', action='store_true')
    args = p.parse_args()
    if args.selfcheck:
        selfcheck()
        return
    import sqlite3
    con = sqlite3.connect(args.db)
    data = load_universe(con, args.limit_symbols, args.seed)
    mkt = market_log_rv(data)
    horizons = [int(x) for x in args.horizons.split(',') if x.strip()]
    results = {}
    for h in horizons:
        results[h] = evaluate_horizon(data, mkt, h)
    holm_pairs = {}
    for h in horizons:
        for k in ('v1','v2','v3'):
            pval = results[h]['comparisons'].get(k, {}).get('p')
            if pval is not None:
                holm_pairs[f'{k}@h{h}'] = pval
    holm = M.holm(holm_pairs) if holm_pairs else {}
    meta = {
        'manifest_id': 'forecastplan-exp-v1',
        'generated_at_utc': '',
        'universe_size': len(T.universe(con)),
        'symbols_used': sum(1 for v in data.values() if len(v['rv']) >= 530),
        'limit': args.limit_symbols,
        'seed': args.seed,
        'duration_s': 0
    }
    write_outputs(results, holm, meta, args.out_dir)
    min_days = min((r['n_days'] for r in results.values() if r['n_days']>0), default=0)
    print(f'VOL RESEARCH OK horizons={horizons} symbols={meta["symbols_used"]} days={min_days}')

def synthetic_bars(seed, n=900):
    rng = np.random.default_rng(seed)
    logp = np.cumsum(rng.normal(0, 0.02, n)) + 4.6
    close = np.exp(logp)
    open_ = np.roll(close, 1) * np.exp(rng.normal(0, 0.005, n))
    open_[0] = close[0]
    hi = np.maximum(open_, close) * np.exp(np.abs(rng.normal(0, 0.01, n)))
    lo = np.minimum(open_, close) * np.exp(-np.abs(rng.normal(0, 0.01, n)))
    return [(1_600_000_000 + i*86400, float(open_[i]), float(hi[i]), float(lo[i]), float(close[i])) for i in range(n)]

def selfcheck():
    data = {}
    for s in range(1,5):
        bars = synthetic_bars(s, n=900)
        ts, rv, _ = T.rv_series(bars)
        data[s] = {'ts': ts, 'rv': rv, 'closes': [b[4] for b in bars]}
    mkt = market_log_rv(data, min_symbols=2)
    r = evaluate_horizon(data, mkt, 1)
    assert r['n_days'] > 100
    assert all(np.isfinite(v) for v in r['mean_qlike'].values())
    assert all(np.isfinite(c['mean_diff']) for c in r['comparisons'].values() if c['mean_diff'] is not None)
    tmp = tempfile.mkdtemp()
    write_outputs({1: r}, M.holm({f'{k}@h1': r['comparisons'][k]['p'] for k in ('v1','v2','v3') if r['comparisons'][k]['p'] is not None}), {'manifest_id':'selfcheck'}, tmp)
    print('SELFCHECK OK')

if __name__ == '__main__':
    main()