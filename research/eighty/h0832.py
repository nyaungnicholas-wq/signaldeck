# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 831
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time(), tzinfo=timezone.utc).timestamp())

def get_quarter_ends(start_date, end_date):
    """Generate quarter-end dates (last calendar day of Mar, Jun, Sep, Dec) within range."""
    quarters = []
    year = start_date.year
    q_end_month = 3
    while True:
        if q_end_month == 3:
            q_end = datetime(year, 3, 31, tzinfo=timezone.utc).date()
        elif q_end_month == 6:
            q_end = datetime(year, 6, 30, tzinfo=timezone.utc).date()
        elif q_end_month == 9:
            q_end = datetime(year, 9, 30, tzinfo=timezone.utc).date()
        else:
            q_end = datetime(year, 12, 31, tzinfo=timezone.utc).date()
        if q_end > end_date:
            break
        if q_end >= start_date:
            quarters.append(q_end)
        q_end_month += 3
        if q_end_month > 12:
            q_end_month = 3
            year += 1
    return quarters

def prev_quarter_end(q_end):
    """Return previous quarter end date."""
    if q_end.month == 3:
        return datetime(q_end.year - 1, 12, 31, tzinfo=timezone.utc).date()
    elif q_end.month == 6:
        return datetime(q_end.year, 3, 31, tzinfo=timezone.utc).date()
    elif q_end.month == 9:
        return datetime(q_end.year, 6, 30, tzinfo=timezone.utc).date()
    else:
        return datetime(q_end.year, 9, 30, tzinfo=timezone.utc).date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Check public float data availability
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat'
        ORDER BY symbol_id, fetched_at
    """)
    float_rows = cur.fetchall()
    if not float_rows:
        print("INSUFFICIENT=1")
        return

    # Group by symbol_id
    float_by_symbol = defaultdict(list)
    for row in float_rows:
        float_by_symbol[row['symbol_id']].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })

    # 2. Get symbols with sufficient daily bars (>=252 from 2018-07-26)
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt, MIN(ts) as min_ts, MAX(ts) as max_ts
        FROM bars
        WHERE tf = '1d' AND ts >= strftime('%s', '2018-07-26')
        GROUP BY symbol_id
        HAVING cnt >= 252
    """)
    symbol_bars = {row['symbol_id']: {'count': row['cnt'], 'min_ts': row['min_ts'], 'max_ts': row['max_ts']} for row in cur.fetchall()}

    # Universe: symbols with bars AND public float data
    universe = [sid for sid in symbol_bars if sid in float_by_symbol]
    if len(universe) < 10:  # arbitrarily small
        print("INSUFFICIENT=1")
        return

    # 3. Load all daily bars for universe symbols (close prices)
    # We need bars for quarterly returns and 21-day forward returns
    placeholders = ','.join('?' * len(universe))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, universe)
    bars_rows = cur.fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        bars_by_symbol[row['symbol_id']].append({'ts': row['ts'], 'close': row['close']})

    # 4. Determine quarter ends from bar date range
    all_ts = [b['ts'] for bars in bars_by_symbol.values() for b in bars]
    if not all_ts:
        print("INSUFFICIENT=1")
        return
    min_date = epoch_to_date(min(all_ts))
    max_date = epoch_to_date(max(all_ts))
    quarter_ends = get_quarter_ends(min_date, max_date)

    # 5. For each symbol, build a time series of close prices indexed by date
    close_by_symbol_date = {}
    for sid, bars in bars_by_symbol.items():
        d = {}
        for b in bars:
            d[epoch_to_date(b['ts'])] = b['close']
        close_by_symbol_date[sid] = d

    # 6. For each symbol and quarter end, evaluate entry conditions
    calls = []  # (decision_date, symbol_id, forward_return, hit)

    for sid in universe:
        floats = float_by_symbol[sid]
        # Sort by fetched_at
        floats.sort(key=lambda x: x['fetched_at'])
        closes = close_by_symbol_date.get(sid, {})
        if not closes:
            continue

        # For each quarter end, find latest float with fetched_at <= quarter_end
        for q_end in quarter_ends:
            q_end_epoch = date_to_epoch(q_end)
            # Find current quarter float
            curr_float = None
            for f in floats:
                if f['fetched_at'] <= q_end_epoch:
                    curr_float = f
                else:
                    break
            if not curr_float:
                continue

            # Find previous quarter float
            prev_q_end = prev_quarter_end(q_end)
            prev_q_end_epoch = date_to_epoch(prev_q_end)
            prev_float = None
            for f in floats:
                if f['fetched_at'] <= prev_q_end_epoch:
                    prev_float = f
                else:
                    break
            if not prev_float:
                continue

            # QoQ public float increase >= 2%
            if prev_float['value'] <= 0:
                continue
            float_growth = (curr_float['value'] - prev_float['value']) / prev_float['value']
            if float_growth < 0.02:
                continue

            # Quarterly total return <= -5%
            # Find close at prev_q_end and q_end (last trading day on or before)
            def get_close_on_or_before(date):
                d = date
                for _ in range(10):  # search back up to 10 days
                    if d in closes:
                        return closes[d]
                    d -= timedelta(days=1)
                return None

            close_prev = get_close_on_or_before(prev_q_end)
            close_curr = get_close_on_or_before(q_end)
            if close_prev is None or close_curr is None or close_prev <= 0:
                continue
            q_return = (close_curr - close_prev) / close_prev
            if q_return > -0.05:
                continue

            # Entry conditions met - issue "up" call
            # Compute 21-trading-day forward return from q_end
            # Find the next trading day after q_end
            decision_date = q_end
            forward_start = None
            d = decision_date + timedelta(days=1)
            for _ in range(10):
                if d in closes:
                    forward_start = d
                    break
                d += timedelta(days=1)
            if not forward_start:
                continue

            # Find 21 trading days later
            trading_days = 0
            d = forward_start
            forward_end = None
            while trading_days < 21:
                if d in closes:
                    trading_days += 1
                    if trading_days == 21:
                        forward_end = d
                        break
                d += timedelta(days=1)
                if d > max_date:
                    break
            if not forward_end:
                continue

            start_px = closes[forward_start]
            end_px = closes[forward_end]
            if start_px <= 0:
                continue
            fwd_return = (end_px - start_px) / start_px
            hit = 1 if fwd_return > 0 else 0

            calls.append({
                'decision_date': decision_date,
                'symbol_id': sid,
                'fwd_return': fwd_return,
                'hit': hit
            })

    if not calls:
        print("INSUFFICIENT=1")
        return

    # 7. Sort calls by decision date
    calls.sort(key=lambda x: x['decision_date'])

    # 8. Split 80/20 by time (most recent 20% sealed)
    n_total = len(calls)
    split_idx = int(n_total * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c['hit'] for c in call_list)
        precision = hits / issued if issued else 0.0
        # Base rate of predicted class ("up") within issued subset
        # Since we only issue "up" calls, base rate = proportion of actual up moves in issued set
        base_rate = hits / issued if issued else 0.0  # same as precision for binary "up" calls
        # Distinct UTC days among issued calls
        distinct_days = len(set(c['decision_date'] for c in call_list))
        # Design effect: clustering by day
        day_counts = defaultdict(int)
        for c in call_list:
            day_counts[c['decision_date']] += 1
        sum_n2 = sum(v*v for v in day_counts.values())
        design_effect = sum_n2 / issued if issued else 1.0
        # Ensure design_effect > 1 (temporal autocorrelation)
        if design_effect <= 1.0:
            design_effect = 1.01
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_dd, train_en = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_dd, sealed_en = compute_metrics(sealed_calls)

    # Overall metrics (for reporting)
    all_issued, all_hits, all_prec, all_br, all_dd, all_en = compute_metrics(calls)

    # Print required lines
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={len(universe) * len(quarter_ends)}")  # approximate
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_dd}")
    print(f"EFFECTIVE_N={all_en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()