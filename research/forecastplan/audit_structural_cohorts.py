import argparse
import csv
import json
import os
import sqlite3
import sys
import tempfile
import datetime
from collections import defaultdict

def compute(con, epoch_ts):
    cur = con.cursor()
    cur.execute("""
        SELECT id, kind, ts, day, horizon_days, regime, conviction,
               historical_accuracy, rank, resolved_at, actual, correct,
               naive_label, revision, basis_epoch, superseded_by, settle_ts,
               ungradable
        FROM regime_outcomes
    """)
    rows = cur.fetchall()

    # per-kind accumulators
    kind_stats = defaultdict(lambda: {
        'n_resolved_all': 0, 'sum_correct': 0,
        'n_frozen': 0, 'sum_correct_frozen': 0, 'sum_naive_eq_actual_frozen': 0,
        'n_unfrozen': 0, 'sum_correct_unfrozen': 0,
        'n_post_epoch': 0, 'sum_correct_post': 0,
        'n_pre_epoch': 0, 'sum_correct_pre': 0,
        'n_superseded': 0, 'sum_correct_excl_superseded': 0,
        'day_set': set(), 'min_ts': None, 'max_ts': None,
        'n_total': 0, 'n_unresolved': 0
    })
    # per-day accumulators: kind -> day -> dict
    day_stats = defaultdict(lambda: defaultdict(lambda: {
        'n': 0, 'sum_correct': 0,
        'n_frozen': 0, 'sum_naive_eq_actual': 0,
        'n_superseded': 0
    }))

    for r in rows:
        (_, kind, ts, _, _, _, _, _, _, _, actual, correct,
         naive_label, _, _, superseded_by, _, _) = r
        stats = kind_stats[kind]
        stats['n_total'] += 1
        resolved = correct is not None
        if resolved:
            stats['n_resolved_all'] += 1
            stats['sum_correct'] += correct
            day_str = datetime.datetime.fromtimestamp(ts, datetime.timezone.utc).strftime('%Y-%m-%d')
            stats['day_set'].add(day_str)
            if stats['min_ts'] is None or ts < stats['min_ts']:
                stats['min_ts'] = ts
            if stats['max_ts'] is None or ts > stats['max_ts']:
                stats['max_ts'] = ts
            if superseded_by is not None:
                stats['n_superseded'] += 1
            else:
                stats['sum_correct_excl_superseded'] += correct
            # epoch split
            if ts >= epoch_ts:
                stats['n_post_epoch'] += 1
                stats['sum_correct_post'] += correct
            else:
                stats['n_pre_epoch'] += 1
                stats['sum_correct_pre'] += correct
            # frozen/unfrozen
            if naive_label is not None:
                stats['n_frozen'] += 1
                stats['sum_correct_frozen'] += correct
                if actual is not None and naive_label == actual:
                    stats['sum_naive_eq_actual_frozen'] += 1
            else:
                stats['n_unfrozen'] += 1
                stats['sum_correct_unfrozen'] += correct
            # per day
            dstat = day_stats[kind][day_str]
            dstat['n'] += 1
            dstat['sum_correct'] += correct
            if naive_label is not None:
                dstat['n_frozen'] += 1
                if actual is not None and naive_label == actual:
                    dstat['sum_naive_eq_actual'] += 1
            if superseded_by is not None:
                dstat['n_superseded'] += 1
        else:
            stats['n_unresolved'] += 1

    # Build output lists
    per_kind = []
    per_day = []
    for kind, s in kind_stats.items():
        n_res = s['n_resolved_all']
        acc_all = s['sum_correct'] / n_res if n_res else 0.0
        n_froz = s['n_frozen']
        acc_froz = s['sum_correct_frozen'] / n_froz if n_froz else 0.0
        n_unfroz = s['n_unfrozen']
        acc_unfroz = s['sum_correct_unfrozen'] / n_unfroz if n_unfroz else 0.0
        naive_acc_froz = s['sum_naive_eq_actual_frozen'] / n_froz if n_froz else 0.0
        n_post = s['n_post_epoch']
        acc_post = s['sum_correct_post'] / n_post if n_post else 0.0
        n_pre = s['n_pre_epoch']
        acc_pre = s['sum_correct_pre'] / n_pre if n_pre else 0.0
        n_sup = s['n_superseded']
        denom_excl = n_res - n_sup
        acc_excl = s['sum_correct_excl_superseded'] / denom_excl if denom_excl else 0.0
        n_distinct = len(s['day_set'])
        first_day = datetime.datetime.fromtimestamp(s['min_ts'], datetime.timezone.utc).strftime('%Y-%m-%d') if s['min_ts'] is not None else ''
        last_day = datetime.datetime.fromtimestamp(s['max_ts'], datetime.timezone.utc).strftime('%Y-%m-%d') if s['max_ts'] is not None else ''
        per_kind.append({
            'kind': kind,
            'n_resolved_all': n_res,
            'acc_all': round(acc_all, 4),
            'n_frozen': n_froz,
            'acc_frozen': round(acc_froz, 4),
            'n_unfrozen': n_unfroz,
            'acc_unfrozen': round(acc_unfroz, 4),
            'naive_acc_frozen': round(naive_acc_froz, 4),
            'n_post_epoch': n_post,
            'acc_post_epoch': round(acc_post, 4),
            'n_pre_epoch': n_pre,
            'acc_pre_epoch': round(acc_pre, 4),
            'n_superseded': n_sup,
            'acc_excluding_superseded': round(acc_excl, 4),
            'n_distinct_call_days': n_distinct,
            'first_call_day': first_day,
            'last_call_day': last_day,
            'n_total_rows': s['n_total'],
            'n_unresolved': s['n_unresolved']
        })
        for day_str, ds in day_stats[kind].items():
            n_day = ds['n']
            acc_day = ds['sum_correct'] / n_day if n_day else 0.0
            n_froz_day = ds['n_frozen']
            naive_acc_day = ds['sum_naive_eq_actual'] / n_froz_day if n_froz_day else 0.0
            per_day.append({
                'kind': kind,
                'day': day_str,
                'n': n_day,
                'acc': round(acc_day, 4),
                'n_frozen': n_froz_day,
                'naive_acc': round(naive_acc_day, 4),
                'n_superseded': ds['n_superseded']
            })
    return per_kind, per_day

def write_outputs(per_kind, per_day, out_dir):
    os.makedirs(out_dir, exist_ok=True)
    # structural_cohorts.csv
    kind_fields = ['kind','n_resolved_all','acc_all','n_frozen','acc_frozen',
                   'n_unfrozen','acc_unfrozen','naive_acc_frozen',
                   'n_post_epoch','acc_post_epoch','n_pre_epoch','acc_pre_epoch',
                   'n_superseded','acc_excluding_superseded','n_distinct_call_days',
                   'first_call_day','last_call_day','n_total_rows','n_unresolved']
    with open(os.path.join(out_dir, 'structural_cohorts.csv'), 'w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=kind_fields)
        writer.writeheader()
        writer.writerows(per_kind)
    # structural_cohorts_by_day.csv
    day_fields = ['kind','day','n','acc','n_frozen','naive_acc','n_superseded']
    with open(os.path.join(out_dir, 'structural_cohorts_by_day.csv'), 'w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=day_fields)
        writer.writeheader()
        writer.writerows(per_day)
    # structural_cohorts_summary.json
    summary = {}
    for k in per_kind:
        kind = k['kind']
        summary[kind] = {field: k[field] for field in kind_fields if field != 'kind'}
        summary[kind]['diagnostic_minus_frozen_pp'] = round(100 * (k['acc_all'] - k['acc_frozen']), 2)
    summary['_meta'] = {
        'generated_at_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'db_path': getattr(write_outputs, 'db_path', ''),
        'epoch': getattr(write_outputs, 'epoch_str', '')
    }
    with open(os.path.join(out_dir, 'structural_cohorts_summary.json'), 'w') as f:
        json.dump(summary, f, indent=1, sort_keys=True)

def selfcheck():
    import datetime
    epoch_str = '2026-07-27'
    epoch_dt = datetime.datetime.strptime(epoch_str, '%Y-%m-%d').replace(tzinfo=datetime.timezone.utc)
    epoch_ts = int(epoch_dt.timestamp())
    with tempfile.TemporaryDirectory() as tmpdir:
        db_path = os.path.join(tmpdir, 'test.db')
        con = sqlite3.connect(db_path)
        cur = con.cursor()
        cur.execute('''
            CREATE TABLE regime_outcomes(
                id INT PRIMARY KEY,
                symbol_id INT,
                kind TEXT,
                ts INT,
                day INT,
                horizon_days INT,
                regime TEXT,
                conviction REAL,
                historical_accuracy REAL,
                rank REAL,
                resolved_at INT,
                actual TEXT,
                correct INT,
                naive_label TEXT,
                revision TEXT,
                basis_epoch INT,
                superseded_by INT,
                settle_ts INT,
                ungradable TEXT
            )
        ''')
        # Insert rows
        # 4 pre-epoch resolved, naive_label NULL, correct 1,1,1,0
        base_ts_pre = epoch_ts - 86400  # one day before
        for i, corr in enumerate([1,1,1,0]):
            cur.execute('''
                INSERT INTO regime_outcomes (kind, ts, day, correct, actual, naive_label, superseded_by)
                VALUES (?, ?, ?, ?, ?, ?, NULL)
            ''', ('vol21', base_ts_pre + i*86400, int((base_ts_pre + i*86400)/86400), corr, 'UP', None))
        # 4 post-epoch resolved, naive_label set, correct 0,0,1,0, naive_acc 0.5
        base_ts_post = epoch_ts
        # We'll set actual and naive_label such that matches in rows 2 and 4 (0-index)
        post_data = [
            (0, 'DOWN', 'UP'),   # mismatch
            (0, 'UP', 'DOWN'),   # mismatch
            (1, 'UP', 'UP'),     # match
            (0, 'DOWN', 'DOWN')  # match
        ]
        for i, (corr, actual, naive) in enumerate(post_data):
            cur.execute('''
                INSERT INTO regime_outcomes (kind, ts, day, correct, actual, naive_label, superseded_by)
                VALUES (?, ?, ?, ?, ?, ?, NULL)
            ''', ('vol21', base_ts_post + i*86400, int((base_ts_post + i*86400)/86400), corr, actual, naive))
        # 1 unresolved row (any time)
        cur.execute('''
            INSERT INTO regime_outcomes (kind, ts, day, correct, actual, naive_label, superseded_by)
            VALUES (?, ?, ?, NULL, NULL, NULL, NULL)
        ''', ('vol21', epoch_ts + 86400*10, int((epoch_ts + 86400*10)/86400)))
        con.commit()
        # Run compute
        per_kind, per_day = compute(con, epoch_ts)
        # Validate
        assert len(per_kind) == 1
        k = per_kind[0]
        assert k['kind'] == 'vol21'
        assert k['acc_all'] == 0.5
        assert k['acc_frozen'] == 0.25
        assert k['acc_unfrozen'] == 0.75
        assert k['n_frozen'] == 4
        assert k['naive_acc_frozen'] == 0.5
        assert k['n_unresolved'] == 1
        assert round(100 * (k['acc_all'] - k['acc_frozen']), 2) == 25.0
        con.close()  # Windows: an open handle blocks TemporaryDirectory cleanup
        print('SELFCHECK OK')

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', default='data/signaldeck.db')
    parser.add_argument('--out-dir', default='research/forecastplan/out')
    parser.add_argument('--epoch', default='2026-07-27')
    parser.add_argument('--selfcheck', action='store_true')
    args = parser.parse_args()
    if args.selfcheck:
        selfcheck()
        return
    epoch_dt = datetime.datetime.strptime(args.epoch, '%Y-%m-%d').replace(tzinfo=datetime.timezone.utc)
    epoch_ts = int(epoch_dt.timestamp())
    con = sqlite3.connect('file:' + args.db + '?mode=ro', uri=True)
    try:
        per_kind, per_day = compute(con, epoch_ts)
        # attach meta for write_outputs
        write_outputs.db_path = args.db
        write_outputs.epoch_str = args.epoch
        write_outputs(per_kind, per_day, args.out_dir)
        total_resolved = sum(k['n_resolved_all'] for k in per_kind)
        print(f'STRUCTURAL COHORTS OK kinds={len(per_kind)} rows_resolved={total_resolved}')
    finally:
        con.close()

if __name__ == '__main__':
    main()