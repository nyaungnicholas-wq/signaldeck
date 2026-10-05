import sqlite3
import sys
from datetime import datetime, date
from collections import defaultdict
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    trades = conn.execute("""
        SELECT symbol_id, filed_ts, title
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%President%')
        ORDER BY filed_ts
    """).fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return

    trade_list = []
    for t in trades:
        T = datetime.utcfromtimestamp(t['filed_ts']).date()
        trade_list.append({
            'symbol_id': t['symbol_id'],
            'filed_ts': t['filed_ts'],
            'T': T,
            'title': t['title']
        })

    symbols_needed = set(t['symbol_id'] for t in trade_list)
    decision_days = sorted(set(t['T'] for t in trade_list))

    placeholders = ','.join('?' for _ in symbols_needed)
    bars_rows = conn.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(symbols_needed)).fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        day = datetime.utcfromtimestamp(row['ts']).date()
        bars_by_symbol[row['symbol_id']].append({
            'day': day,
            'ts': row['ts'],
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume']
        })

    stats_by_symbol = {}
    all_vols_by_day = defaultdict(list)

    for sym_id, bars in bars_by_symbol.items():
        if len(bars) < 252:
            continue
        n = len(bars)
        closes = [b['close'] for b in bars]
        highs = [b['high'] for b in bars]
        volumes = [b['volume'] for b in bars]
        days = [b['day'] for b in bars]
        tss = [b['ts'] for b in bars]

        daily_rets = [0.0] * n
        for i in range(1, n):
            if closes[i-1] != 0:
                daily_rets[i] = (closes[i] - closes[i-1]) / closes[i-1]

        high_252 = [0.0] * n
        for i in range(n):
            start = max(0, i - 251)
            high_252[i] = max(highs[start:i+1])

        ret_20 = [0.0] * n
        for i in range(20, n):
            if closes[i-20] != 0:
                ret_20[i] = closes[i] / closes[i-20] - 1

        avg_dollar_vol_60 = [0.0] * n
        for i in range(59, n):
            total = sum(closes[j] * volumes[j] for j in range(i-59, i+1))
            avg_dollar_vol_60[i] = total / 60

        vol_20 = [0.0] * n
        for i in range(19, n):
            rets = daily_rets[i-19:i+1]
            mean_ret = sum(rets) / 20
            var = sum((r - mean_ret)**2 for r in rets) / 20
            vol_20[i] = math.sqrt(var)
            all_vols_by_day[days[i]].append(vol_20[i])

        stats_by_symbol[sym_id] = {
            'days': days,
            'tss': tss,
            'closes': closes,
            'high_252': high_252,
            'ret_20': ret_20,
            'avg_dollar_vol_60': avg_dollar_vol_60,
            'vol_20': vol_20,
            'daily_rets': daily_rets
        }

    vol_90th_by_day = {}
    for day, vols in all_vols_by_day.items():
        if len(vols) >= 10:
            vols_sorted = sorted(vols)
            idx = int(len(vols_sorted) * 0.9)
            vol_90th_by_day[day] = vols_sorted[idx]
        else:
            vol_90th_by_day[day] = float('inf')

    po_rows = conn.execute("""
        SELECT symbol_id, horizon, ts, up
        FROM prediction_outcomes
        WHERE horizon = 20
    """).fetchall()

    po_by_symbol = defaultdict(list)
    for row in po_rows:
        po_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'up': row['up']
        })
    for sym in po_by_symbol:
        po_by_symbol[sym].sort(key=lambda x: x['ts'])

    issued_calls = []
    opportunities = 0
    last_call_bar_idx = {}

    for trade in trade_list:
        opportunities += 1
        sym_id = trade['symbol_id']
        T = trade['T']
        filed_ts = trade['filed_ts']

        if sym_id not in stats_by_symbol:
            continue
        stats = stats_by_symbol[sym_id]
        days = stats['days']

        idx = -1
        for i, d in enumerate(days):
            if d < T:
                idx = i
            else:
                break

        if idx < 251:
            continue

        close_T_1 = stats['closes'][idx]
        if close_T_1 < 5:
            continue

        high_252_T_1 = stats['high_252'][idx]
        if high_252_T_1 == 0:
            continue
        drawdown = (close_T_1 - high_252_T_1) / high_252_T_1
        if drawdown > -0.20:
            continue

        ret_20_T_1 = stats['ret_20'][idx]
        if ret_20_T_1 > -0.15:
            continue

        if idx < 59:
            continue
        avg_dv = stats['avg_dollar_vol_60'][idx]
        if avg_dv < 5_000_000:
            continue

        vol_20_T_1 = stats['vol_20'][idx]
        day_T_1 = days[idx]
        vol_90th = vol_90th_by_day.get(day_T_1, float('inf'))
        if vol_20_T_1 > vol_90th:
            continue

        last_idx = last_call_bar_idx.get(sym_id)
        if last_idx is not None and idx - last_idx < 20:
            continue

        po_list = po_by_symbol.get(sym_id, [])
        label = None
        for po in po_list:
            if po['ts'] <= filed_ts:
                label = po['up']
            else:
                break

        if label is None:
            continue

        issued_calls.append({
            'symbol_id': sym_id,
            'T': T,
            'filed_ts': filed_ts,
            'day_T_1': day_T_1,
            'hit': 1 if label == 1 else 0
        })
        last_call_bar_idx[sym_id] = idx

    if len(issued_calls) < 30:
        print("INSUFFICIENT=1")
        return

    issued_calls.sort(key=lambda x: x['filed_ts'])
    n_total = len(issued_calls)
    split_idx = int(n_total * 0.8)
    train_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c['hit'] for c in calls)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0
        distinct_days = len(set(c['T'] for c in calls))

        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c['T']] += 1
        cluster_sizes = list(day_counts.values())
        avg_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1

        outcomes = [c['hit'] for c in calls]
        overall_mean = sum(outcomes) / len(outcomes) if outcomes else 0
        between_var = sum(sz * ((sum(c['hit'] for c in calls if c['T'] == day) / sz) - overall_mean)**2
                          for day, sz in day_counts.items()) / len(day_counts) if day_counts else 0
        within_var = sum((c['hit'] - (sum(c2['hit'] for c2 in calls if c2['T'] == c['T']) / day_counts[c['T']]))**2
                         for c in calls) / len(calls) if calls else 0
        total_var = between_var + within_var
        icc = between_var / total_var if total_var > 0 else 0
        design_effect = 1 + (avg_cluster - 1) * icc if avg_cluster > 1 else 1
        effective_n = issued / design_effect if design_effect > 0 else issued

        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_train, hits_train, prec_train, base_train, distinct_train, eff_train = compute_metrics(train_calls)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, distinct_sealed, eff_sealed = compute_metrics(sealed_calls)

    issued_all = len(issued_calls)
    hits_all = sum(c['hit'] for c in issued_calls)
    precision_all = hits_all / issued_all if issued_all > 0 else 0.0
    base_rate_all = hits_all / issued_all if issued_all > 0 else 0.0
    distinct_all = len(set(c['T'] for c in issued_calls))

    day_counts_all = defaultdict(int)
    for c in issued_calls:
        day_counts_all[c['T']] += 1
    cluster_sizes_all = list(day_counts_all.values())
    avg_cluster_all = sum(cluster_sizes_all) / len(cluster_sizes_all) if cluster_sizes_all else 1
    outcomes_all = [c['hit'] for c in issued_calls]
    overall_mean_all = sum(outcomes_all) / len(outcomes_all) if outcomes_all else 0
    between_var_all = sum(sz * ((sum(c['hit'] for c in issued_calls if c['T'] == day) / sz) - overall_mean_all)**2
                          for day, sz in day_counts_all.items()) / len(day_counts_all) if day_counts_all else 0
    within_var_all = sum((c['hit'] - (sum(c2['hit'] for c2 in issued_calls if c2['T'] == c['T']) / day_counts_all[c['T']]))**2
                         for c in issued_calls) / len(issued_calls) if issued_calls else 0
    total_var_all = between_var_all + within_var_all
    icc_all = between_var_all / total_var_all if total_var_all > 0 else 0
    design_effect_all = 1 + (avg_cluster_all - 1) * icc_all if avg_cluster_all > 1 else 1
    effective_n_all = issued_all / design_effect_all if design_effect_all > 0 else issued_all

    print(f"ISSUED={issued_all}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_all:.6f}")
    print(f"BASE_RATE={base_rate_all:.6f}")
    print(f"DISTINCT_DAYS={distinct_all}")
    print(f"EFFECTIVE_N={effective_n_all:.6f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

if __name__ == "__main__":
    main()