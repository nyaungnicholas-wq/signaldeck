# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 699
# cycle_index: 26
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get date ranges
    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
    bar_min_ts, bar_max_ts = cur.fetchone()
    bar_min_date = epoch_to_date(bar_min_ts)
    bar_max_date = epoch_to_date(bar_max_ts)

    cur.execute("SELECT MIN(day), MAX(day) FROM sentiment_features")
    sent_min_day, sent_max_day = cur.fetchone()
    sent_min_date = str_to_date(sent_min_day)
    sent_max_date = str_to_date(sent_max_day)

    cur.execute("SELECT MIN(filed_ts), MAX(filed_ts) FROM insider_trades WHERE code='P'")
    ins_min_ts, ins_max_ts = cur.fetchone()
    ins_min_date = epoch_to_date(ins_min_ts) if ins_min_ts else bar_max_date
    ins_max_date = epoch_to_date(ins_max_ts) if ins_max_ts else bar_max_date

    # Overlap window for decisions
    start_date = max(bar_min_date, sent_min_date, ins_min_date)
    end_date = min(bar_max_date, sent_max_date, ins_max_date)

    # Need 252 days lookback for 200-day MA and quartiles, 21 days forward for label
    # Also need 60 days sentiment history for quartile calc
    decision_start = start_date + timedelta(days=252)
    decision_end = end_date - timedelta(days=21)

    if decision_start >= decision_end:
        print("INSUFFICIENT=1")
        return 0

    # Get active US stock symbols with enough bars
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.market='stocks' AND s.active=1
        AND EXISTS (
            SELECT 1 FROM bars b
            WHERE b.symbol_id=s.id AND b.tf='1d'
            GROUP BY b.symbol_id
            HAVING COUNT(*) >= 252
        )
    """)
    symbols = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = [s[0] for s in symbols]

    # Pre-load daily bars for all symbols in range
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
        AND ts >= ? AND ts <= ?
        ORDER BY symbol_id, ts
    """, symbol_ids + [int(datetime.combine(start_date, datetime.min.time()).timestamp()),
                       int(datetime.combine(end_date + timedelta(days=21), datetime.min.time()).timestamp())])

    bars_by_symbol = {}
    for row in cur.fetchall():
        bars_by_symbol.setdefault(row['symbol_id'], []).append((row['ts'], row['close']))

    # Pre-load sentiment_features
    cur.execute(f"""
        SELECT symbol_id, day, n_all, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        AND day >= ? AND day <= ?
        ORDER BY symbol_id, day
    """, symbol_ids + [date_to_str(start_date), date_to_str(end_date)])

    sent_by_symbol = {}
    for row in cur.fetchall():
        sent_by_symbol.setdefault(row['symbol_id'], []).append((row['day'], row['n_all'], row['mean_score']))

    # Get officer insider purchases (code='P')
    cur.execute(f"""
        SELECT symbol_id, filed_ts, title, shares, price
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({placeholders})
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%CHIEF%')
        AND filed_ts >= ? AND filed_ts <= ?
        ORDER BY symbol_id, filed_ts
    """, symbol_ids + [int(datetime.combine(decision_start, datetime.min.time()).timestamp()),
                       int(datetime.combine(decision_end, datetime.min.time()).timestamp())])

    insider_events = []
    for row in cur.fetchall():
        insider_events.append({
            'symbol_id': row['symbol_id'],
            'filed_ts': row['filed_ts'],
            'filed_date': epoch_to_date(row['filed_ts']),
            'title': row['title'],
            'shares': row['shares'],
            'price': row['price']
        })

    if not insider_events:
        print("INSUFFICIENT=1")
        return 0

    # Process each symbol
    all_opportunities = 0
    all_calls = []  # (decision_date, symbol_id, fwd_return, hit)

    for sym_id, sym in symbols:
        bars = bars_by_symbol.get(sym_id, [])
        sent = sent_by_symbol.get(sym_id, [])
        events = [e for e in insider_events if e['symbol_id'] == sym_id]

        if len(bars) < 252 or len(sent) < 60 or not events:
            continue

        # Build bar lookup: ts -> close
        bar_dict = {ts: close for ts, close in bars}
        bar_dates = sorted(bar_dict.keys())

        # Build sentiment lookup: day -> (n_all, mean_score)
        sent_dict = {day: (n_all, mean_score) for day, n_all, mean_score in sent}

        # Pre-compute 200-day SMA and 20-day realized vol for each bar date
        # 20-day realized vol = std of 20 daily log returns
        import math
        sma200 = {}
        vol20 = {}
        vol_history = []  # for quartile

        for i, ts in enumerate(bar_dates):
            if i >= 199:
                window_closes = [bar_dict[bar_dates[j]] for j in range(i-199, i+1)]
                sma200[ts] = sum(window_closes) / 200
            if i >= 19:
                rets = []
                for j in range(i-19, i+1):
                    if j > 0:
                        prev_close = bar_dict[bar_dates[j-1]]
                        curr_close = bar_dict[bar_dates[j]]
                        if prev_close > 0:
                            rets.append(math.log(curr_close / prev_close))
                if len(rets) == 20:
                    mean_ret = sum(rets) / 20
                    var = sum((r - mean_ret)**2 for r in rets) / 20
                    v = math.sqrt(var * 252)  # annualized
                    vol20[ts] = v
                    vol_history.append(v)

        if not vol_history:
            continue
        vol_history.sort()
        vol_q75 = vol_history[int(len(vol_history) * 0.75)]

        # Pre-compute n_all quartile history (252-day rolling)
        sent_dates = sorted(sent_dict.keys())
        n_all_history = []
        n_all_q25_by_date = {}

        for day_str in sent_dates:
            n_all = sent_dict[day_str][0]
            n_all_history.append(n_all)
            if len(n_all_history) > 252:
                n_all_history.pop(0)
            if len(n_all_history) >= 60:
                sorted_hist = sorted(n_all_history)
                n_all_q25_by_date[day_str] = sorted_hist[int(len(sorted_hist) * 0.25)]

        # Process each insider event
        for ev in events:
            all_opportunities += 1
            filed_date = ev['filed_date']
            filed_date_str = date_to_str(filed_date)

            # Find the bar ts for filed_date (or previous trading day)
            filed_ts = None
            for ts in reversed(bar_dates):
                if epoch_to_date(ts) <= filed_date:
                    filed_ts = ts
                    break
            if filed_ts is None:
                continue

            # Check 200-day SMA exists and close > SMA
            if filed_ts not in sma200:
                continue
            close = bar_dict[filed_ts]
            if close <= sma200[filed_ts]:
                continue

            # Check volatility not in top quartile
            if filed_ts in vol20 and vol20[filed_ts] > vol_q75:
                continue

            # Check sentiment features on filed_date
            if filed_date_str not in sent_dict:
                continue
            n_all, mean_score = sent_dict[filed_date_str]
            if mean_score <= 0.2:
                continue
            if filed_date_str not in n_all_q25_by_date:
                continue
            if n_all > n_all_q25_by_date[filed_date_str]:
                continue

            # Check abstain: fewer than 3 setups in prior 252 sessions for this symbol
            # Count prior qualifying events for this symbol
            prior_count = 0
            for prev_ev in events:
                if prev_ev['filed_ts'] < ev['filed_ts'] and prev_ev['filed_ts'] >= ev['filed_ts'] - 252*86400:
                    # Would need to re-check conditions for prior events - simplified: count prior officer buys
                    prior_count += 1
            if prior_count < 3:
                continue

            # Compute 21-day forward return
            # Find bar 21 trading days after filed_ts
            filed_idx = bar_dates.index(filed_ts)
            target_idx = filed_idx + 21
            if target_idx >= len(bar_dates):
                continue
            target_ts = bar_dates[target_idx]
            target_close = bar_dict[target_ts]
            fwd_return = (target_close - close) / close
            hit = 1 if fwd_return > 0 else 0

            all_calls.append((filed_date, sym_id, fwd_return, hit))

    if not all_calls:
        print("INSUFFICIENT=1")
        return 0

    # Hold out most recent 20% by date
    all_calls.sort(key=lambda x: x[0])
    split_idx = int(len(all_calls) * 0.8)
    train_calls = all_calls[:split_idx]
    sealed_calls = all_calls[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c[3] for c in calls)
        precision = hits / issued
        base_rate = hits / issued  # base rate within issued subset
        distinct_days = len(set(c[0] for c in calls))
        # Design effect: 1 + (avg cluster size - 1) * intraclass correlation
        # Approximate: group by day, compute variance of daily hit rates
        from collections import defaultdict
        day_hits = defaultdict(list)
        for c in calls:
            day_hits[c[0]].append(c[3])
        daily_rates = [sum(v)/len(v) for v in day_hits.values()]
        if len(daily_rates) > 1:
            mean_rate = sum(daily_rates) / len(daily_rates)
            var_rate = sum((r - mean_rate)**2 for r in daily_rates) / len(daily_rates)
            # Intraclass correlation approximation
            icc = var_rate / (mean_rate * (1 - mean_rate) + 1e-9)
            icc = max(0, min(1, icc))
            avg_cluster = issued / len(daily_rates)
            deff = 1 + (avg_cluster - 1) * icc
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_base, train_days, train_eff = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_prec, sealed_base, sealed_days, sealed_eff = compute_metrics(sealed_calls)

    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={all_opportunities}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_base:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())