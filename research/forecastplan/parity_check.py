import sys, os, json, subprocess, argparse, math, random, pathlib, datetime, sqlite3, hashlib

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', type=str, default=None)
    parser.add_argument('--n-stocks', type=int, default=25)
    parser.add_argument('--seed', type=int, default=7)
    parser.add_argument('--go', type=str, default='go')
    parser.add_argument('--repo', type=str, default=None)
    parser.add_argument('--out', type=str, default=None)
    parser.add_argument('--min-bars', type=int, default=300)
    args = parser.parse_args()

    script_dir = pathlib.Path(__file__).resolve().parent
    if args.repo is None:
        repo_dir = script_dir.parent.parent
    else:
        repo_dir = pathlib.Path(args.repo).resolve()
    if args.db is None:
        db_path = repo_dir / 'data' / 'signaldeck.db'
    else:
        db_path = pathlib.Path(args.db).resolve()
    if args.out is None:
        out_dir = repo_dir / 'research' / 'forecastplan' / 'out'
    else:
        out_dir = pathlib.Path(args.out).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    if sys.platform.startswith('win'):
        sys.stdout.reconfigure(encoding='utf-8')

    sys.path.insert(0, str(script_dir))
    import labels

    # ---------- construct cases ----------
    cases = []  # stores original data (with NaN as float('nan')) for Python calls
    def add_case(cid, closes, volumes, t, horizon):
        cases.append({
            'id': cid,
            'closes': closes,
            'volumes': volumes,
            't': t,
            'horizon_days': horizon
        })

    # 1. c_const
    add_case('c_const', [100.0]*300, [1000.0]*300, 250, 21)

    # 2. c_rise21, c_rise63
    rise_closes = [100.0 + 0.1*i for i in range(400)]
    rise_vols = [1000.0]*400
    add_case('c_rise21', rise_closes, rise_vols, 300, 21)
    add_case('c_rise63', rise_closes, rise_vols, 300, 63)

    # 3. c_short
    short_closes = [100.0 + 0.1*i for i in range(150)]
    short_vols = [1000.0]*150
    add_case('c_short', short_closes, short_vols, 100, 21)

    # 4. c_edge_ok, c_edge_bad
    edge_closes = [100.0 + 0.1*i for i in range(400)]
    edge_vols = [1000.0]*400
    add_case('c_edge_ok', edge_closes, edge_vols, 378, 21)
    add_case('c_edge_bad', edge_closes, edge_vols, 379, 21)

    # 5. c_fwd_zero_vol, c_trail_zero_vol
    fwd_vols = [1000.0]*400
    fwd_vols[310] = 0.0
    add_case('c_fwd_zero_vol', edge_closes, fwd_vols, 300, 21)
    trail_vols = [1000.0]*400
    trail_vols[100] = 0.0
    add_case('c_trail_zero_vol', edge_closes, trail_vols, 300, 21)

    # 6. c_nan_close
    nan_closes = [100.0 + 0.1*i for i in range(400)]
    nan_closes[250] = float('nan')
    nan_closes[320] = float('nan')
    add_case('c_nan_close', nan_closes, edge_vols, 300, 21)

    # 7. c_neg_t, c_t_zero
    base_closes = [100.0 + 0.1*i for i in range(400)]
    base_vols = [1000.0]*400
    add_case('c_neg_t', base_closes, base_vols, -1, 21)
    add_case('c_t_zero', base_closes, base_vols, 0, 21)

    # 8. c_walk_k_t
    for k in range(5):
        rng = random.Random(args.seed + k)
        closes = [50.0]
        for _ in range(1, 600):
            closes.append(closes[-1] * math.exp(rng.gauss(0.0, 0.02)))
        volumes = [math.exp(rng.gauss(12.0, 0.8)) for _ in range(600)]
        for t in (400, 578, 199, 200):
            add_case(f'c_walk_{k}_{t}', closes, volumes, t, 21)

    # ---------- real cases ----------
    conn = sqlite3.connect(f'file:{db_path}?mode=ro', uri=True)
    try:
        cur = conn.cursor()
        cur.execute('SELECT id, symbol, market FROM symbols')
        all_symbols = {row[0]: {'symbol': row[1], 'market': row[2]} for row in cur.fetchall()}
        crypto_ids = [sid for sid, info in all_symbols.items() if info['market'] == 'crypto']
        cur.execute('''SELECT symbol_id, COUNT(*) FROM bars
                       WHERE tf='1d' AND close > 0
                       GROUP BY symbol_id
                       HAVING COUNT(*) >= ?''', (args.min_bars,))
        eligible_ids = [row[0] for row in cur.fetchall()]
        rnd = random.Random(args.seed)
        sample_k = min(args.n_stocks, len(eligible_ids))
        sampled = rnd.sample(eligible_ids, sample_k) if eligible_ids else []
        symbol_ids = set(crypto_ids) | set(sampled)
        for sid in symbol_ids:
            info = all_symbols[sid]
            sym = info['symbol']
            cur.execute('''SELECT close, volume FROM bars
                           WHERE symbol_id=? AND tf='1d' AND close > 0
                           ORDER BY ts''', (sid,))
            rows = cur.fetchall()
            if not rows:
                continue
            closes = [float(r[0]) for r in rows]
            volumes = [float(r[1]) for r in rows]
            n = len(closes)
            if n < 30:
                continue
            cand = {n-22, n-100, n-250, 250, 200, 199, 25}
            cand = {t for t in cand if 0 <= t < n}
            for t in cand:
                add_case(f'r_{sym}_{t}_{21}', closes, volumes, t, 21)
                if t == n-100:
                    add_case(f'r_{sym}_{t}_{63}', closes, volumes, t, 63)
    finally:
        conn.close()

    n_constructed = sum(1 for c in cases if c['id'].startswith('c_'))
    n_real = len(cases) - n_constructed
    n_symbols_real = len({c['id'].split('_')[1] for c in cases if c['id'].startswith('r_')})

    # Clean cases for JSON (NaN -> None) for Go harness and output
    def clean_lst(lst):
        return [None if (isinstance(x, float) and math.isnan(x)) else x for x in lst]
    cases_for_json = []
    for c in cases:
        cases_for_json.append({
            'id': c['id'],
            'closes': clean_lst(c['closes']),
            'volumes': clean_lst(c['volumes']),
            't': c['t'],
            'horizon_days': c['horizon_days']
        })

    cases_json_path = out_dir / 'parity_cases.json'
    with open(cases_json_path, 'w', encoding='utf-8') as f:
        json.dump({'cases': cases_for_json}, f, ensure_ascii=False, indent=2)

    daemon_dir = repo_dir / 'daemon'
    input_json = json.dumps({'cases': cases_for_json}, allow_nan=False)
    proc = subprocess.run(
        [args.go, 'run', './cmd/label-parity'],
        cwd=str(daemon_dir),
        input=input_json,
        capture_output=True,
        text=True,
        encoding='utf-8'
    )
    if proc.returncode != 0:
        sys.stderr.write(proc.stderr)
        sys.exit(2)
    go_out = json.loads(proc.stdout)
    go_results = {r['id']: r for r in go_out['results']}

    mismatches = []
    func_names = ['trend', 'liquidity', 'vol21', 'naive_trend', 'naive_liquidity', 'naive_vol21']
    stats = {f: {'n': len(cases), 'n_ok_both': 0, 'n_mismatch': 0} for f in func_names}

    for c in cases:
        cid = c['id']
        go_res = go_results.get(cid)
        if go_res is None:
            continue
        try:
            py_res = labels.resolve_all(c['closes'], c['volumes'], c['t'], c['horizon_days'])
        except Exception:
            py_res = {func: None for func in func_names}
        for func in func_names:
            py_val = py_res.get(func)
            go_val = go_res.get(func)
            if func.startswith('naive_'):
                py_ok = py_val is not None
                go_ok = go_val.get('ok', False) if isinstance(go_val, dict) else False
                if py_ok != go_ok:
                    mismatches.append({'id': cid, 'function': func, 'python': py_val, 'go': go_val})
                    stats[func]['n_mismatch'] += 1
                elif py_ok:
                    if py_val == go_val.get('label'):
                        stats[func]['n_ok_both'] += 1
                    else:
                        mismatches.append({'id': cid, 'function': func, 'python': py_val, 'go': go_val})
                        stats[func]['n_mismatch'] += 1
                else:
                    stats[func]['n_ok_both'] += 1
            else:
                py_ok = py_val is not None
                go_ok = go_val.get('ok', False) if isinstance(go_val, dict) else False
                if py_ok != go_ok:
                    mismatches.append({'id': cid, 'function': func, 'python': py_val, 'go': go_val})
                    stats[func]['n_mismatch'] += 1
                elif py_ok:
                    ok_match = True
                    py_act = py_val.get('actual')
                    go_act = go_val.get('actual')
                    if isinstance(py_act, (int, float)) and isinstance(go_act, (int, float)):
                        if not math.isclose(py_act, go_act, rel_tol=1e-12, abs_tol=1e-12):
                            ok_match = False
                    else:
                        if py_act != go_act:
                            ok_match = False
                    if ok_match:
                        if py_val.get('key_name') != go_val.get('key_name'):
                            ok_match = False
                    if ok_match:
                        py_kv = py_val.get('key_value')
                        go_kv = go_val.get('key_value')
                        if isinstance(py_kv, (int, float)) and isinstance(go_kv, (int, float)):
                            if not math.isclose(py_kv, go_kv, rel_tol=1e-12, abs_tol=1e-12):
                                ok_match = False
                        else:
                            if py_kv != go_kv:
                                ok_match = False
                    if ok_match:
                        stats[func]['n_ok_both'] += 1
                    else:
                        mismatches.append({'id': cid, 'function': func, 'python': py_val, 'go': go_val})
                        stats[func]['n_mismatch'] += 1
                else:
                    stats[func]['n_ok_both'] += 1

    def sha256_file(p):
        h = hashlib.sha256()
        with open(p, 'rb') as f:
            for chunk in iter(lambda: f.read(8192), b''):
                h.update(chunk)
        return h.hexdigest()
    labels_py_sha = sha256_file(script_dir / 'labels.py')
    go_main_sha = sha256_file(daemon_dir / 'cmd' / 'label-parity' / 'main.go')

    go_ver_proc = subprocess.run([args.go, 'version'], capture_output=True, text=True, encoding='utf-8')
    go_version = go_ver_proc.stdout.strip() if go_ver_proc.returncode == 0 else 'unknown'

    report = {
        'generated_at_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'n_cases': len(cases),
        'n_constructed': n_constructed,
        'n_real': n_real,
        'n_symbols_real': n_symbols_real,
        'go_version': go_version,
        'per_function': {f: {'n': stats[f]['n'], 'n_ok_both': stats[f]['n_ok_both'], 'n_mismatch': stats[f]['n_mismatch']} for f in func_names},
        'mismatches': mismatches[:50],
        'labels_py_sha256': labels_py_sha,
        'go_main_sha256': go_main_sha
    }
    report_path = out_dir / 'parity_report.json'
    with open(report_path, 'w', encoding='utf-8') as f:
        json.dump(report, f, ensure_ascii=False, indent=2)

    for f in func_names:
        okb = stats[f]['n_ok_both']
        tot = stats[f]['n']
        mis = stats[f]['n_mismatch']
        print(f'{f}: ok_both={okb}/{tot} mismatches={mis}')
    if len(mismatches) == 0:
        print(f'PARITY OK cases={len(cases)} functions=6 mismatches=0')
        sys.exit(0)
    else:
        print(f'PARITY FAIL cases={len(cases)} mismatches={len(mismatches)}')
        sys.exit(1)

if __name__ == '__main__':
    main()