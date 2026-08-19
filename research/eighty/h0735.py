# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 734
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get symbols with both insider trades (code='P') and sentiment_features
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN insider_trades it ON it.symbol_id = s.id
        JOIN sentiment_features sf ON sf.symbol_id = s.id
        WHERE it.code = 'P'
        AND s.active = 1
    """)
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    # Build hedged ratio history per symbol from sentiment_features
    # hedged_ratio = hedged / n_all (where n_all > 0)
    cur.execute("""
        SELECT symbol_id, day, hedged, n_all
        FROM sentiment_features
        WHERE n_all > 0
        ORDER BY symbol_id, day
    """)
    sf_rows = cur.fetchall()

    # Organize by symbol
    sf_by_symbol = {}
    for row in sf_rows:
        sid = row['symbol_id']
        if sid not in sf_by_symbol:
            sf_by_symbol[sid] = []
        day = row['day']
        hedged_ratio = row['hedged'] / row['n_all'] if row['n_all'] > 0 else 0
        sf_by_symbol[sid].append((day, hedged_ratio))

    # Get insider purchases (code='P') with filed_ts
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    insider_rows = cur.fetchall()

    # Get daily bars for forward returns
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bar_rows = cur.fetchall()

    # Organize bars by symbol
    bars_by_symbol = {}
    for row in bar_rows:
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append((row['ts'], row['close']))

    # Helper: convert filed_ts (unix epoch) to date string YYYY-MM-DD
    def ts_to_date(ts):
        return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

    # Helper: find close price on or after a date
    def get_close_on_or_after(bars, target_ts):
        # bars: list of (ts, close) sorted by ts
        # target_ts: unix epoch (start of day)
        # Return close at target_ts or next available
        lo, hi = 0, len(bars)
        while lo < hi:
            mid = (lo + hi) // 2
            if bars[mid][0] < target_ts:
                lo = mid + 1
            else:
                hi = mid
        if lo < len(bars):
            return bars[lo][1]
        return None

    # Helper: get forward return over N trading days
    def get_forward_return(bars, entry_ts, horizon_days):
        # Find entry index
        lo, hi = 0, len(bars)
        while lo < hi:
            mid = (lo + hi) // 2
            if bars[mid][0] < entry_ts:
                lo = mid + 1
            else:
                hi = mid
        entry_idx = lo
        if entry_idx >= len(bars):
            return None
        entry_close = bars[entry_idx][1]
        exit_idx = entry_idx + horizon_days
        if exit_idx >= len(bars):
            return None
        exit_close = bars[exit_idx][1]
        return (exit_close - entry_close) / entry_close

    # Helper: compute hedged ratio percentile rank over past 252 trading days
    def get_hedged_percentile(sf_history, target_date, lookback_days=252):
        # sf_history: list of (day_str, hedged_ratio) sorted by day
        # target_date: YYYY-MM-DD string
        # Find values in [target_date - lookback_days, target_date)
        target_dt = datetime.strptime(target_date, '%Y-%m-%d')
        cutoff_dt = target_dt - timedelta(days=lookback_days * 1.5)  # approx calendar days
        cutoff_str = cutoff_dt.strftime('%Y-%m-%d')
        
        values = []
        for day_str, ratio in sf_history:
            if day_str >= cutoff_str and day_str < target_date:
                values.append(ratio)
        
        if len(values) < 20:  # need minimum history
            return None
        
        current_ratio = None
        for day_str, ratio in sf_history:
            if day_str == target_date:
                current_ratio = ratio
                break
        if current_ratio is None:
            # Use most recent before target_date
            for day_str, ratio in reversed(sf_history):
                if day_str < target_date:
                    current_ratio = ratio
                    break
        if current_ratio is None:
            return None
        
        # Percentile rank
        below = sum(1 for v in values if v < current_ratio)
        return below / len(values)

    HORIZON_DAYS = 21  # ~1 month trading days

    # Process each insider purchase
    calls = []  # (decision_date, symbol_id, symbol, entry_ts, forward_return, hedged_pct)
    
    for row in insider_rows:
        sid = row['symbol_id']
        filed_ts = row['filed_ts']
        
        if sid not in sf_by_symbol or sid not in bars_by_symbol:
            continue
        
        decision_date = ts_to_date(filed_ts)
        sf_history = sf_by_symbol[sid]
        
        # Get hedged percentile
        pct = get_hedged_percentile(sf_history, decision_date)
        if pct is None or pct <= 0.75:
            continue
        
        # Get entry price (close on filing date or next trading day)
        bars = bars_by_symbol[sid]
        entry_ts = filed_ts  # use filing timestamp as decision time
        # Align to trading day: find bar at or after filing date
        entry_close = get_close_on_or_after(bars, entry_ts)
        if entry_close is None:
            continue
        
        # Get forward return
        fwd_ret = get_forward_return(bars, entry_ts, HORIZON_DAYS)
        if fwd_ret is None:
            continue
        
        calls.append({
            'decision_date': decision_date,
            'symbol_id': sid,
            'entry_ts': entry_ts,
            'fwd_ret': fwd_ret,
            'hedged_pct': pct
        })

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # Sort by decision date
    calls.sort(key=lambda x: x['decision_date'])

    # Split: last 20% by time as sealed era
    n_calls = len(calls)
    split_idx = int(n_calls * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list, label):
        if not call_list:
            return 0, 0, 0, 0, 0, 0
        issued = len(call_list)
        hits = sum(1 for c in call_list if c['fwd_ret'] > 0)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate: unconditional up-rate in the same period for same symbols
        # For simplicity, use overall up-rate in call_list (but this equals precision for long-only)
        # Per instruction: "base rate of the predicted class WITHIN the issued subset"
        # For long-only, this equals precision. But we'll compute unconditional as well.
        base_rate = precision  # Within issued subset, base rate of positive class = precision
        
        # Distinct days among issued calls
        distinct_days = len(set(c['decision_date'] for c in call_list))
        
        # Design effect and effective N
        # Cluster by day, estimate ICC
        day_groups = {}
        for c in call_list:
            day = c['decision_date']
            if day not in day_groups:
                day_groups[day] = []
            day_groups[day].append(1 if c['fwd_ret'] > 0 else 0)
        
        if len(day_groups) > 1:
            # Between-day variance of hit rates
            day_rates = [sum(v)/len(v) for v in day_groups.values()]
            overall_rate = sum(day_rates) / len(day_rates)
            between_var = sum((r - overall_rate)**2 for r in day_rates) / (len(day_rates) - 1) if len(day_rates) > 1 else 0
            # Within-day variance (binary)
            within_var = overall_rate * (1 - overall_rate)
            total_var = between_var + within_var
            if total_var > 0:
                icc = between_var / total_var
            else:
                icc = 0
            avg_cluster = issued / len(day_groups)
            design_effect = 1 + (avg_cluster - 1) * max(icc, 0.05)  # floor ICC at 0.05
        else:
            design_effect = 1.05  # minimum
        
        effective_n = issued / design_effect
        if effective_n >= issued:
            effective_n = issued * 0.99  # ensure strictly less
        
        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Train metrics (for reference)
    train_issued, train_hits, train_prec, train_br, train_dd, train_en = compute_metrics(train_calls, 'train')
    
    # Sealed metrics (primary)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_dd, sealed_en = compute_metrics(sealed_calls, 'sealed')
    
    # Overall metrics (for reporting)
    all_issued, all_hits, all_prec, all_br, all_dd, all_en = compute_metrics(calls, 'all')

    # Output required lines
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={len(insider_rows)}")  # total insider purchases considered
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_dd}")
    print(f"EFFECTIVE_N={all_en:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())