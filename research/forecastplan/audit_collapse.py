import sys
import pathlib
import sqlite3
import re
import json
import tempfile
import os

sys.path.insert(0, pathlib.Path(__file__).parent)
import auditlib

def process_predictions(conn):
    cur = conn.cursor()
    cur.execute("""
        WITH d AS (
            SELECT symbol_id, horizon, ts, raw_prob, cal_prob, n_used,
                   (ts - 18000) / 86400 AS tday,
                   ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, (ts - 18000) / 86400 ORDER BY ts DESC) AS rn
            FROM predictions
        )
        SELECT horizon, tday, raw_prob, cal_prob, n_used
        FROM d WHERE rn = 1
    """)
    rows = cur.fetchall()
    groups = {}
    for horizon, tday, raw_prob, cal_prob, n_used in rows:
        key = (horizon, tday)
        groups.setdefault(key, []).append((raw_prob, cal_prob, n_used))
    fractions = {k/m for m in range(1, 31) for k in range(0, m+1)}
    out = []
    for (horizon, tday), vals in groups.items():
        n_symbols = len(vals)
        raw_probs = [v[0] for v in vals]
        cal_probs = [v[1] for v in vals]
        n_used_list = [v[2] for v in vals]
        n_distinct_raw = len(set(raw_probs))
        n_distinct_cal = len(set(cal_probs))
        n_distinct_n_used = len(set(n_used_list))
        share_n_used_0 = sum(1 for n in n_used_list if n == 0) / n_symbols
        from collections import Counter
        cal_counts = Counter(cal_probs)
        top1_cal_prob, top1_count = cal_counts.most_common(1)[0]
        top3 = sum(count for _, count in cal_counts.most_common(3))
        top3_share = top3 / n_symbols
        top1_raws = set(raw_prob for raw_prob, cal_prob, _ in vals if cal_prob == top1_cal_prob)
        n_distinct_raw_among_top1_cal = len(top1_raws)
        share_raw_small = sum(1 for r in raw_probs if any(abs(r - f) < 1e-9 for f in fractions)) / n_symbols
        collapsed = 1 if n_symbols >= auditlib.MIN_SYMBOLS_FOR_COLLAPSE and n_distinct_cal * 10 < n_symbols else 0
        out.append({
            'horizon': horizon,
            'day': auditlib.day_str(tday),
            'n_symbols': n_symbols,
            'n_distinct_raw': n_distinct_raw,
            'n_distinct_cal': n_distinct_cal,
            'n_distinct_n_used': n_distinct_n_used,
            'share_n_used_0': round(share_n_used_0, 4),
            'top1_cal_prob': top1_cal_prob,
            'top1_count': top1_count,
            'top3_share': round(top3_share, 4),
            'n_distinct_raw_among_top1_cal': n_distinct_raw_among_top1_cal,
            'share_raw_small_fraction': round(share_raw_small, 4),
            'collapsed': collapsed
        })
    out.sort(key=lambda x: (x['horizon'], x['day']))
    return out

def process_outcomes(conn):
    cur = conn.cursor()
    cur.execute("""
        SELECT symbol_id, horizon, ts, prob, up, settle_ts
        FROM prediction_outcomes
        WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL AND ts >= ?
    """, (auditlib.SURVIVORSHIP_EPOCH_TS,))
    rows = cur.fetchall()
    dedup = {}
    for symbol_id, horizon, ts, prob, up, settle_ts in rows:
        day = auditlib.settle_day(settle_ts, ts)
        key = (symbol_id, horizon, day)
        if key not in dedup or ts > dedup[key][0]:
            dedup[key] = (ts, prob, up)
    groups = {}
    for (symbol_id, horizon, day), (ts, prob, up) in dedup.items():
        key = (horizon, day)
        groups.setdefault(key, []).append((prob, up))
    out = []
    for (horizon, day), vals in groups.items():
        n = len(vals)
        probs = [v[0] for v in vals]
        ups = [v[1] for v in vals]
        n_distinct_prob = len(set(probs))
        from collections import Counter
        prob_counts = Counter(probs)
        top1_prob, top1_count = prob_counts.most_common(1)[0]
        top3 = sum(count for _, count in prob_counts.most_common(3))
        top3_share = top3 / n
        acc = sum(1 for p, u in vals if (p >= 0.5) == (u == 1)) / n
        up_share = sum(u for _, u in vals) / n
        hc_n = sum(1 for p, _ in vals if abs(p - 0.5) >= auditlib.HC_CUTOFF)
        if hc_n > 0:
            hc_acc = sum(1 for p, u in vals if abs(p - 0.5) >= auditlib.HC_CUTOFF and (p >= 0.5) == (u == 1)) / hc_n
        else:
            hc_acc = 0.0
        collapsed = 1 if n >= auditlib.MIN_SYMBOLS_FOR_COLLAPSE and n_distinct_prob * 10 < n else 0
        out.append({
            'horizon': horizon,
            'day': auditlib.day_str(day),
            'n': n,
            'n_distinct_prob': n_distinct_prob,
            'top1_prob': top1_prob,
            'top1_count': top1_count,
            'top3_share': round(top3_share, 4),
            'acc': round(acc, 4),
            'up_share': round(up_share, 4),
            'hc_n': hc_n,
            'hc_acc': round(hc_acc, 4),
            'collapsed': collapsed
        })
    out.sort(key=lambda x: (x['horizon'], x['day']))
    return out

def run(conn, out_dir, registry_path):
    out_dir = pathlib.Path(out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    pred_rows = process_predictions(conn)
    auditlib.write_csv(out_dir / 'collapse_trace_predictions.csv', pred_rows, [
        'horizon', 'day', 'n_symbols', 'n_distinct_raw', 'n_distinct_cal', 'n_distinct_n_used',
        'share_n_used_0', 'top1_cal_prob', 'top1_count', 'top3_share',
        'n_distinct_raw_among_top1_cal', 'share_raw_small_fraction', 'collapsed'
    ])
    out_rows = process_outcomes(conn)
    auditlib.write_csv(out_dir / 'collapse_trace_outcomes.csv', out_rows, [
        'horizon', 'day', 'n', 'n_distinct_prob', 'top1_prob', 'top1_count', 'top3_share',
        'acc', 'up_share', 'hc_n', 'hc_acc', 'collapsed'
    ])
    registry = {}
    if pathlib.Path(registry_path).exists():
        with open(registry_path, 'r', encoding='utf-8') as f:
            registry = json.load(f)
    reason = registry.get('refusal_reason', '')
    pattern = r'(1d|1w) (\d{4}-\d{2}-\d{2}) \((\d+) distinct across (\d+) symbols\)'
    matches = re.findall(pattern, reason)
    flagged = []
    n_exact = 0
    n_near = 0
    n_missing = 0
    out_dict = {(r['horizon'], r['day']): r for r in out_rows}
    for horizon, day_str, reg_distinct_str, reg_n_str in matches:
        reg_distinct = int(reg_distinct_str)
        reg_n = int(reg_n_str)
        trace_row = out_dict.get((horizon, day_str))
        if trace_row is None:
            trace_distinct = None
            trace_n = None
            n_missing += 1
        else:
            trace_distinct = trace_row['n_distinct_prob']
            trace_n = trace_row['n']
        exact = (trace_distinct == reg_distinct and trace_n == reg_n) if trace_distinct is not None else False
        near = False
        if trace_distinct is not None and trace_n is not None:
            d_diff = abs(trace_distinct - reg_distinct)
            n_diff = abs(trace_n - reg_n)
            d_thresh = max(3, 0.05 * reg_distinct)
            n_thresh = max(3, 0.05 * reg_n)
            if d_diff <= d_thresh and n_diff <= n_thresh:
                near = True
        if exact:
            n_exact += 1
        if near:
            n_near += 1
        flagged.append({
            'horizon': horizon,
            'day': day_str,
            'registry_distinct': reg_distinct,
            'registry_n': reg_n,
            'trace_distinct': trace_distinct,
            'trace_n': trace_n,
            'exact_match': exact,
            'near_match': near
        })
    n_trace_collapsed = {}
    for row in out_rows:
        hz = row['horizon']
        if row['collapsed'] == 1:
            n_trace_collapsed[hz] = n_trace_collapsed.get(hz, 0) + 1
    report = {
        'registry_status': registry.get('status'),
        'registry_refused_since': registry.get('refused_since'),
        'n_flagged': len(matches),
        'n_exact': n_exact,
        'n_near': n_near,
        'n_missing_in_trace': n_missing,
        'flagged': flagged,
        'n_trace_days_collapsed_flag': n_trace_collapsed
    }
    auditlib.write_json(out_dir / 'collapse_registry_match.json', report)
    print(f"COLLAPSE OK pred_days={len(pred_rows)} outcome_days={len(out_rows)} flagged={len(matches)} exact={n_exact}")

def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', type=str, help='Path to SQLite database')
    parser.add_argument('--out', type=str, help='Output directory')
    parser.add_argument('--registry', type=str, help='Path to accuracy_registry.json')
    parser.add_argument('--repo', type=str, help='Repository root directory')
    parser.add_argument('--selfcheck', action='store_true', help='Run selfcheck')
    args = parser.parse_args()
    if args.selfcheck:
        selfcheck()
        return
    repo = pathlib.Path(args.repo) if args.repo else auditlib.repo_root(__file__)
    db_path = pathlib.Path(args.db) if args.db else repo / 'data' / 'signaldeck.db'
    out_dir = pathlib.Path(args.out) if args.out else repo / 'research' / 'forecastplan' / 'out'
    registry_path = pathlib.Path(args.registry) if args.registry else repo / 'data' / 'accuracy_registry.json'
    conn = auditlib.open_ro(db_path)
    try:
        run(conn, out_dir, registry_path)
    finally:
        conn.close()

def selfcheck():
    import tempfile
    import shutil
    tmpdir = tempfile.mkdtemp()
    try:
        conn = auditlib.fixture_db()
        registry_path = pathlib.Path(tmpdir) / 'registry.json'
        run(conn, tmpdir, registry_path)
        pred_path = pathlib.Path(tmpdir) / 'collapse_trace_predictions.csv'
        out_path = pathlib.Path(tmpdir) / 'collapse_trace_outcomes.csv'
        reg_path = pathlib.Path(tmpdir) / 'collapse_registry_match.json'
        import csv
        with open(pred_path, 'r', encoding='utf-8') as f:
            pred_rows = list(csv.DictReader(f))
        assert len(pred_rows) == 2
        row_1d = [r for r in pred_rows if r['horizon'] == '1d'][0]
        assert row_1d['day'] == auditlib.FIXTURE_PRED_DAY
        assert int(row_1d['n_symbols']) == 42
        assert int(row_1d['n_distinct_cal']) == 3
        assert float(row_1d['top1_cal_prob']) == 0.6
        assert int(row_1d['top1_count']) == 40
        assert int(row_1d['n_distinct_raw_among_top1_cal']) == 10
        assert float(row_1d['share_n_used_0']) == round(4/42, 4)
        assert int(row_1d['collapsed']) == 1
        row_1w = [r for r in pred_rows if r['horizon'] == '1w'][0]
        assert int(row_1w['n_symbols']) == 1
        assert int(row_1w['collapsed']) == 0
        with open(out_path, 'r', encoding='utf-8') as f:
            out_rows = list(csv.DictReader(f))
        assert len(out_rows) == 1
        out_row = out_rows[0]
        assert out_row['horizon'] == '1d'
        assert out_row['day'] == auditlib.FIXTURE_OUTCOME_DAY
        assert int(out_row['n']) == 5
        assert float(out_row['acc']) == 0.8
        assert float(out_row['up_share']) == 1.0
        assert int(out_row['hc_n']) == 1
        assert float(out_row['hc_acc']) == 0.0
        assert int(out_row['n_distinct_prob']) == 2
        registry_data = {
            "status": "REFUSED",
            "refusal_reason": f"gate: 1 collapsed: 1d {auditlib.FIXTURE_OUTCOME_DAY} (2 distinct across 5 symbols)"
        }
        with open(registry_path, 'w', encoding='utf-8') as f:
            json.dump(registry_data, f)
        run(conn, tmpdir, registry_path)
        with open(reg_path, 'r', encoding='utf-8') as f:
            report = json.load(f)
        assert report['n_flagged'] == 1
        assert report['n_exact'] == 1
        assert report['n_missing_in_trace'] == 0
        registry_data2 = {
            "status": "REFUSED",
            "refusal_reason": "gate: 1 collapsed: 1d 2020-01-01 (2 distinct across 5 symbols)"
        }
        with open(registry_path, 'w', encoding='utf-8') as f:
            json.dump(registry_data2, f)
        run(conn, tmpdir, registry_path)
        with open(reg_path, 'r', encoding='utf-8') as f:
            report = json.load(f)
        assert report['n_missing_in_trace'] == 1
        print("SELFCHECK OK")
    finally:
        shutil.rmtree(tmpdir)

if __name__ == "__main__":
    auditlib.stdout_utf8()
    main()