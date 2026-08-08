# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 360
# cycle_index: 28
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'data/signaldeck.db'
HORIZON = 21
YIELD_CHANGE_THRESHOLD = 0.20  # 20 basis points
YIELD_LOOKBACK = 20
INSIDER_MIN = 3
INSIDER_LOOKBACK = 5

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    try:
        conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
    except Exception as e:
        print(f"ERROR: Cannot open database: {e}")
        return

    # Get all symbols with both daily bars and insider trades (code='P')
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        WHERE EXISTS (
            SELECT 1 FROM bars b 
            WHERE b.symbol_id = s.id AND b.tf = '1d'
        ) AND EXISTS (
            SELECT 1 FROM insider_trades i 
            WHERE i.symbol_id = s.id AND i.code = 'P'
        )
    """)
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return

    symbol_ids = [s['id'] for s in symbols]
    symbol_map = {s['id']: s['symbol'] for s in symbols}

    # Get yield curve data (10Y-2Y spread) from macro_series
    cur.execute("""
        SELECT ts, series, value 
        FROM macro_series 
        WHERE series IN ('DGS10', 'DGS2')
        ORDER BY ts
    """)
    yield_data = defaultdict(dict)
    for row in cur.fetchall():
        yield_data[row['ts']][row['series']] = row['value']

    if not yield_data:
        print("INSUFFICIENT=1")
        return

    yield_dates = sorted(yield_data.keys())
    spread_by_ts = {}
    for ts in yield_dates:
        if 'DGS10' in yield_data[ts] and 'DGS2' in yield_data[ts]:
            spread_by_ts[ts] = yield_data[ts]['DGS10'] - yield_data[ts]['DGS2']

    if len(spread_by_ts) < YIELD_LOOKBACK:
        print("INSUFFICIENT=1")
        return

    # Get insider trades (using filed_ts for as-of discipline)
    cur.execute("""
        SELECT symbol_id, code, filed_ts, tx_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    insider_trades = cur.fetchall()

    # Index trades by symbol_id
    trades_by_symbol = defaultdict(list)
    for trade in insider_trades:
        trades_by_symbol[trade['symbol_id']].append(trade['filed_ts'])

    # Get labels from prediction_outcomes for 21-day horizon
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
        ORDER BY ts
    """)
    labels = cur.fetchall()
    if not labels:
        print("INSUFFICIENT=1")
        return

    # Index labels by (symbol_id, ts)
    labels_by_symbol = defaultdict(dict)
    for row in labels:
        labels_by_symbol[row['symbol_id']][row['ts']] = row['up']

    # Get all daily bar timestamps for decision points
    cur.execute("""
        SELECT DISTINCT ts 
        FROM bars 
        WHERE tf = '1d'
        ORDER BY ts
    """)
    bar_dates = [row['ts'] for row in cur.fetchall()]
    if not bar_dates:
        print("INSUFFICIENT=1")
        return

    # Precompute spread changes for each date
    spread_dates = sorted(spread_by_ts.keys())
    spread_change = {}
    for i, ts in enumerate(spread_dates):
        if i >= YIELD_LOOKBACK:
            prev_ts = spread_dates[i - YIELD_LOOKBACK]
            if prev_ts in spread_by_ts:
                spread_change[ts] = spread_by_ts[ts] - spread_by_ts[prev_ts]

    # For each symbol, find decision points where both conditions met
    decisions = []  # (symbol_id, decision_ts, label_ts, label)
    
    for symbol_id in symbol_ids:
        if symbol_id not in trades_by_symbol:
            continue
        trade_ts_list = sorted(trades_by_symbol[symbol_id])
        if symbol_id not in labels_by_symbol:
            continue
        label_ts_set = set(labels_by_symbol[symbol_id].keys())
        
        # For each bar date as potential decision point
        for decision_ts in bar_dates:
            # Check yield curve condition: spread increased >= 20 bps over 20 days
            # Find the most recent spread date <= decision_ts
            spread_ts = None
            for ts in reversed(spread_dates):
                if ts <= decision_ts:
                    spread_ts = ts
                    break
            if spread_ts is None or spread_ts not in spread_change:
                continue
            if spread_change[spread_ts] < YIELD_CHANGE_THRESHOLD:
                continue
            
            # Check insider condition: >= 3 purchases filed in past 5 days
            # Using filed_ts, count trades with filed_ts in (decision_ts - 5 days, decision_ts]
            cutoff_ts = decision_ts - 5 * 86400
            insider_count = sum(1 for ft in trade_ts_list if cutoff_ts < ft <= decision_ts)
            if insider_count < INSIDER_MIN:
                continue
            
            # Label is at decision_ts + 21 days
            label_ts = decision_ts + HORIZON * 86400
            # Find the closest label timestamp (prediction_outcomes ts should match)
            if label_ts in label_ts_set:
                label = labels_by_symbol[symbol_id][label_ts]
                decisions.append((symbol_id, decision_ts, label_ts, label))

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # Sort decisions by decision_ts
    decisions.sort(key=lambda x: x[1])
    
    # Hold out most recent 20% as sealed era
    n_total = len(decisions)
    n_sealed = max(1, int(math.ceil(n_total * 0.2)))
    n_main = n_total - n_sealed
    
    main_decisions = decisions[:n_main]
    sealed_decisions = decisions[n_main:]

    def compute_metrics(dec_list):
        if not dec_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        
        issued = len(dec_list)
        hits = sum(1 for d in dec_list if d[3] == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class (up=1) within issued
        
        # Distinct UTC days among issued calls
        distinct_days = len(set(ts_to_date(d[1]) for d in dec_list))
        
        # Design effect: cluster by day, compute effective N
        day_counts = defaultdict(int)
        for d in dec_list:
            day_counts[ts_to_date(d[1])] += 1
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Using simple approximation: deff = 1 + (mean_cluster_size - 1) * rho
        # With rho estimated from intra-day correlation, but we'll use a conservative estimate
        # For clustered binary data, effective_n = n / deff
        # Simple approach: deff = 1 + (avg_per_day - 1) * 0.5 (conservative ICC)
        if day_counts:
            avg_per_day = sum(day_counts.values()) / len(day_counts)
            deff = 1 + (avg_per_day - 1) * 0.5
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        
        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Opportunities = total decision points considered (symbol-days where we could evaluate)
    # This is the count of (symbol, decision_ts) pairs where we checked conditions
    # We need to count all evaluated points, not just issued
    opportunities = 0
    for symbol_id in symbol_ids:
        if symbol_id not in trades_by_symbol:
            continue
        trade_ts_list = sorted(trades_by_symbol[symbol_id])
        for decision_ts in bar_dates:
            # Check if we could evaluate (have yield data up to decision_ts)
            spread_ts = None
            for ts in reversed(spread_dates):
                if ts <= decision_ts:
                    spread_ts = ts
                    break
            if spread_ts is None or spread_ts not in spread_change:
                continue
            opportunities += 1

    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_decisions)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_decisions)

    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_precision:.6f}")
    print(f"BASE_RATE={main_base_rate:.6f}")
    print(f"DISTINCT_DAYS={main_distinct_days}")
    print(f"EFFECTIVE_N={main_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()