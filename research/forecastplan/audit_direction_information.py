import datetime
import argparse, os, json, csv, sqlite3, math, statistics
from collections import defaultdict

def mann_whitney_auc(probs, labels):
    n = len(probs)
    n_pos = sum(labels)
    n_neg = n - n_pos
    if n_pos == 0 or n_neg == 0:
        return None
    items = list(zip(probs, labels))
    items.sort(key=lambda x: x[0])
    ranks = [0.0]*n
    i = 0
    while i < n:
        j = i
        while j < n and items[j][0] == items[i][0]:
            j += 1
        avg_rank = (i + 1 + j) / 2.0
        for k in range(i, j):
            ranks[k] = avg_rank
        i = j
    sum_pos = sum(ranks[idx] for idx, (_, lbl) in enumerate(items) if lbl == 1)
    auc = (sum_pos - n_pos*(n_pos+1)/2.0) / (n_pos * n_neg)
    return auc

def compute_metrics(rows):
    if not rows:
        return None
    n_rows = len(rows)
    probs = [r[3] for r in rows]
    ups = [r[4] for r in rows]
    trading_days = [r[5] for r in rows]
    n_days = len(set(trading_days))
    # basic accuracy
    acc = sum((p>=0.5)==(u==1) for p,u in zip(probs, ups))/n_rows
    acc_flipped = sum((p<0.5)==(u==1) for p,u in zip(probs, ups))/n_rows
    always_up_acc = sum(ups)/n_rows
    always_down_acc = 1.0 - always_up_acc
    # brier
    brier = sum((p-u)**2 for p,u in zip(probs, ups))/n_rows
    # log loss
    eps = 1e-6
    ll = -sum(u*math.log(max(eps, min(1-eps, p))) + (1-u)*math.log(max(eps, min(1-eps, 1-p))) for p,u in zip(probs, ups))/n_rows
    # auc pooled
    auc_pooled = mann_whitney_auc(probs, ups)
    # balanced accuracy
    tp = sum(1 for p,u in zip(probs, ups) if u==1 and p>=0.5)
    fn = sum(1 for p,u in zip(probs, ups) if u==1 and p<0.5)
    tn = sum(1 for p,u in zip(probs, ups) if u==0 and p<0.5)
    fp = sum(1 for p,u in zip(probs, ups) if u==0 and p>=0.5)
    tpr = tp/(tp+fn) if (tp+fn) else None
    tnr = tn/(tn+fp) if (tn+fp) else None
    bal_acc = 0.5*(tpr+tnr) if tpr is not None and tnr is not None else None
    # prequential majority
    days_map = defaultdict(list)
    for r in rows:
        days_map[r[5]].append(r)
    sorted_days = sorted(days_map.keys())
    cum_up = cum_total = 0
    correct = scored = 0
    for d in sorted_days:
        day_rows = days_map[d]
        day_up = sum(r[4] for r in day_rows)
        day_n = len(day_rows)
        if cum_total == 0:
            skip = True
        else:
            if cum_up*2 > cum_total:
                guess_up = True
            elif cum_up*2 < cum_total:
                guess_up = False
            else:
                skip = True
        if not skip:
            guess = 1 if guess_up else 0
            correct += sum(1 for r in day_rows if r[4]==guess)
            scored += day_n
        cum_up += day_up
        cum_total += day_n
    preq_acc = correct/scored if scored else None
    # mean daily agreement and bet acc
    agree_sum = bet_sum = 0
    day_count = 0
    auc_day_list = []
    for d in sorted_days:
        day_rows = days_map[d]
        day_n = len(day_rows)
        if day_n == 0:
            continue
        day_probs = [r[3] for r in day_rows]
        day_ups = [r[4] for r in day_rows]
        share_up = sum(p>=0.5 for p in day_probs)/day_n
        realized_up = sum(day_ups)/day_n
        agree_sum += max(share_up, 1-share_up)
        bet_sum += (share_up>0.5) == (realized_up>0.5)
        day_count += 1
        if day_n >= 30 and sum(day_ups)>0 and sum(day_ups)<day_n:
            auc_day = mann_whitney_auc(day_probs, day_ups)
            if auc_day is not None:
                auc_day_list.append(auc_day)
    mean_daily_agreement = agree_sum/day_count if day_count else None
    day_bet_acc = bet_sum/day_count if day_count else None
    auc_within_day_mean = statistics.mean(auc_day_list) if auc_day_list else None
    auc_within_day_se = statistics.pstdev(auc_day_list)/math.sqrt(len(auc_day_list)) if len(auc_day_list)>=2 else (0.0 if auc_day_list else None)
    n_days_auc = len(auc_day_list)
    # reliability bins
    bin_sums = [[0.0,0,0] for _ in range(10)]  # sum_prob, sum_up, count
    for p,u in zip(probs, ups):
        if p == 1.0:
            b = 9
        else:
            b = int(p*10)
        if b < 0: b = 0
        if b > 9: b = 9
        bin_sums[b][0] += p
        bin_sums[b][1] += u
        bin_sums[b][2] += 1
    reliability = []
    for b in range(10):
        s, u, c = bin_sums[b]
        if c:
            reliability.append([s/c, u/c, c])
        else:
            reliability.append([0.0,0.0,0])
    return {
        'n_rows': n_rows,
        'n_days': n_days,
        'acc': acc,
        'acc_flipped_diagnostic': acc_flipped,
        'always_up_acc': always_up_acc,
        'always_down_acc': always_down_acc,
        'prequential_majority_acc': preq_acc,
        'balanced_accuracy': bal_acc,
        'brier': brier,
        'log_loss': ll,
        'auc_pooled': auc_pooled,
        'auc_within_day_mean': auc_within_day_mean,
        'auc_within_day_se': auc_within_day_se,
        'n_days_auc': n_days_auc,
        'mean_daily_agreement': mean_daily_agreement,
        'day_bet_acc': day_bet_acc,
        'reliability': reliability
    }

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', default='data/signaldeck.db')
    parser.add_argument('--out-dir', default='research/forecastplan/out')
    parser.add_argument('--selfcheck', action='store_true')
    args = parser.parse_args()
    if args.selfcheck:
        selfcheck()
        return
    os.makedirs(args.out_dir, exist_ok=True)
    conn = sqlite3.connect('file:'+args.db+'?mode=ro', uri=True)
    try:
        cur = conn.cursor()
        cur.execute("""
            WITH filtered AS (
                SELECT symbol_id, horizon, ts, prob, up,
                       CAST((ts-18000)/86400 AS INTEGER) AS trading_day
                FROM prediction_outcomes
                WHERE horizon IN ('1d','1w','1d#pm','1w#pm')
                  AND resolved_at IS NOT NULL
                  AND up IS NOT NULL
                  AND prob IS NOT NULL
                  AND ts >= 1784851200
            ),
            ranked AS (
                SELECT *,
                       ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, trading_day ORDER BY ts DESC) AS rn
                FROM filtered
            )
            SELECT symbol_id, horizon, ts, prob, up, trading_day
            FROM ranked
            WHERE rn = 1
            ORDER BY horizon, trading_day, symbol_id
        """)
        rows = cur.fetchall()
    finally:
        conn.close()
    by_horizon = defaultdict(list)
    for r in rows:
        by_horizon[r[1]].append(r)
    horizon_metrics = {}
    total_n_rows = 0
    csv_path = os.path.join(args.out_dir, 'direction_information_by_day.csv')
    with open(csv_path, 'w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=['horizon','day','n','mean_prob','share_up_calls','realized_up','acc','auc_within'])
        writer.writeheader()
        for hz, hz_rows in by_horizon.items():
            total_n_rows += len(hz_rows)
            metrics = compute_metrics(hz_rows)
            horizon_metrics[hz] = metrics
            # per-day rows
            days_map = defaultdict(list)
            for r in hz_rows:
                days_map[r[5]].append(r)
            for day in sorted(days_map.keys()):
                day_rows = days_map[day]
                n = len(day_rows)
                mean_prob = sum(r[3] for r in day_rows)/n
                share_up_calls = sum(r[3]>=0.5 for r in day_rows)/n
                realized_up = sum(r[4] for r in day_rows)/n
                acc = sum((r[3]>=0.5)==(r[4]==1) for r in day_rows)/n
                # auc_within
                if n >= 30 and sum(r[4] for r in day_rows)>0 and sum(r[4] for r in day_rows)<n:
                    auc_day = mann_whitney_auc([r[3] for r in day_rows], [r[4] for r in day_rows])
                    auc_within = '' if auc_day is None else f'{auc_day:.6f}'
                else:
                    auc_within = ''
                day_iso = (day*86400)  # seconds since epoch
                # convert to UTC date string
                dt = datetime.datetime.utcfromtimestamp(day_iso)
                day_str = dt.date().isoformat()
                writer.writerow({
                    'horizon': hz,
                    'day': day_str,
                    'n': n,
                    'mean_prob': f'{mean_prob:.6f}',
                    'share_up_calls': f'{share_up_calls:.6f}',
                    'realized_up': f'{realized_up:.6f}',
                    'acc': f'{acc:.6f}',
                    'auc_within': auc_within
                })
    summary = {
        'generated_at_utc': datetime.datetime.utcnow().replace(microsecond=0).isoformat()+'Z',
        'db_path': args.db,
        'population_note': 'Filtered to horizons in (\"1d\",\"1w\",\"1d#pm\",\"1w#pm\"), resolved_at NOT NULL, up NOT NULL, prob NOT NULL, ts >= 1784851200, deduplicated to latest per symbol/horizon/trading_day',
        **{hz: horizon_metrics[hz] for hz in sorted(horizon_metrics.keys())}
    }
    json_path = os.path.join(args.out_dir, 'direction_information_summary.json')
    with open(json_path, 'w') as f:
        json.dump(summary, f, indent=1, sort_keys=True)
    print(f'DIRECTION INFO OK horizons={len(by_horizon)} rows={total_n_rows}')

def selfcheck():
    import datetime
    conn = sqlite3.connect(':memory:')
    try:
        cur = conn.cursor()
        cur.execute('''
            CREATE TABLE prediction_outcomes(
                symbol_id INT, horizon TEXT, ts INT, prob REAL, up INT,
                fwd_return REAL, resolved_at INT, basis_epoch INT, settle_ts INT
            )
        ''')
        base = 1784851200
        # fixture 1: 1d
        for day in range(3):
            for sym in range(40):
                ts = base + day*86400 + 50000 + sym
                up = 1 if sym < 24 else 0
                if sym % 5 == 0:
                    prob = 0.25 if up==1 else 0.75
                else:
                    prob = 0.75 if up==1 else 0.25
                cur.execute('INSERT INTO prediction_outcomes VALUES (?,?,?,?,?,0,?,?,?)',
                            (sym, '1d', ts, prob, up, ts+86400, ts, ts+86400))
        # fixture 2: 1w
        for day in range(3):
            for sym in range(40):
                ts = base + day*86400 + 50000 + sym
                up = 1 if sym < 24 else 0
                prob = 0.4
                cur.execute('INSERT INTO prediction_outcomes VALUES (?,?,?,?,?,0,?,?,?)',
                            (sym, '1w', ts, prob, up, ts+86400, ts, ts+86400))
        # duplicate rows for symbol 0 day 0
        cur.execute('INSERT INTO prediction_outcomes VALUES (?,?,?,?,?,0,?,?,?)',
                    (0, '1w', base+0*86400+50000+0, 0.5, 1, base+86400, base, base+86400))
        cur.execute('INSERT INTO prediction_outcomes VALUES (?,?,?,?,?,0,?,?,?)',
                    (0, '1w', base+0*86400+50000+0+1, 0.4, 1, base+86400, base, base+86400))  # later duplicate: dedup must keep it
        conn.commit()
        cur.execute("""
            WITH filtered AS (
                SELECT symbol_id, horizon, ts, prob, up,
                       CAST((ts-18000)/86400 AS INTEGER) AS trading_day
                FROM prediction_outcomes
                WHERE horizon IN ('1d','1w')
                  AND resolved_at IS NOT NULL
                  AND up IS NOT NULL
                  AND prob IS NOT NULL
                  AND ts >= 1784851200
            ),
            ranked AS (
                SELECT *,
                       ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, trading_day ORDER BY ts DESC) AS rn
                FROM filtered
            )
            SELECT symbol_id, horizon, ts, prob, up, trading_day
            FROM ranked
            WHERE rn = 1
        """)
        rows = cur.fetchall()
        by_h = {}
        for r in rows:
            by_h.setdefault(r[1], []).append(r)
        # check 1d
        m1d = compute_metrics(by_h['1d'])
        assert m1d['n_days'] == 3 and m1d['n_rows'] == 120
        assert m1d['acc'] > 0.7
        assert m1d['auc_within_day_mean'] is not None and m1d['auc_within_day_mean'] > 0.75
        # check 1w
        m1w = compute_metrics(by_h['1w'])
        assert abs(m1w['acc'] - 0.4) < 1e-9
        assert abs(m1w['always_up_acc'] - 0.6) < 1e-9
        assert abs(m1w['acc_flipped_diagnostic'] - 0.6) < 1e-9
        assert abs(m1w['auc_within_day_mean'] - 0.5) < 1e-9  # every prob ties, so the tie-averaged AUC is exactly 0.5
        assert abs(m1w['mean_daily_agreement'] - 1.0) < 1e-9
        # duplicate check: ensure n_rows unchanged vs without duplicates
        # compute expected rows without duplicates: 3 days * 40 symbols = 120
        assert m1w['n_rows'] == 120
        print('SELFCHECK OK')
    finally:
        conn.close()

if __name__ == '__main__':
    main()