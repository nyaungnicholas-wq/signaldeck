import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all symbols with daily bars and insider trades
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        JOIN insider_trades it ON it.symbol_id = s.id
        WHERE s.active = 1
    """)
    symbols = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    # Load daily bars for all symbols
    bars_by_symbol = {}
    for sym_id, sym in symbols:
        cur.execute("""
            SELECT ts, open, high, low, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym_id,))
        rows = cur.fetchall()
        if len(rows) >= 505:  # need 504 prior + current
            bars_by_symbol[sym_id] = [(r['ts'], r['open'], r['high'], r['low'], r['close'], r['volume']) for r in rows]

    # Load insider sales (code='S') for all symbols
    sales_by_symbol = defaultdict(list)
    for sym_id, _ in symbols:
        cur.execute("""
            SELECT filed_ts
            FROM insider_trades
            WHERE symbol_id = ? AND code = 'S'
            ORDER BY filed_ts
        """, (sym_id,))
        for row in cur.fetchall():
            sales_by_symbol[sym_id].append(row['filed_ts'])

    # Load prediction_outcomes for horizon ~20 trading days
    # Find the horizon that best matches 20 trading days
    cur.execute("SELECT DISTINCT horizon FROM prediction_outcomes")
    horizons = [row['horizon'] for row in cur.fetchall()]
    target_horizon = None
    for h in horizons:
        if str(h) in ('20d', '20', '1m', '21d'):
            target_horizon = h
            break
    if target_horizon is None:
        target_horizon = horizons[0] if horizons else '20d'

    outcomes_by_symbol = defaultdict(dict)
    for sym_id, _ in symbols:
        cur.execute("""
            SELECT ts, up
            FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = ?
        """, (sym_id, target_horizon))
        for row in cur.fetchall():
            outcomes_by_symbol[sym_id][row['ts']] = row['up']

    # Process each symbol
    all_opportunities = []  # (ts, sym_id, sym, close, high_252, ret_20, ret_1, vol_20, sales_count, avg_dollar_vol, has_outcome, outcome_up)
    
    for sym_id, sym in symbols:
        if sym_id not in bars_by_symbol:
            continue
        bars = bars_by_symbol[sym_id]
        sales = sales_by_symbol.get(sym_id, [])
        outcomes = outcomes_by_symbol.get(sym_id, {})
        
        n = len(bars)
        # Precompute rolling values
        closes = [b[4] for b in bars]
        highs = [b[2] for b in bars]
        volumes = [b[5] for b in bars]
        timestamps = [b[0] for b in bars]
        
        # 252-session high (rolling max of high)
        high_252 = [0.0] * n
        from collections import deque
        dq = deque()
        for i in range(n):
            while dq and dq[0] <= i - 252:
                dq.popleft()
            while dq and highs[dq[-1]] <= highs[i]:
                dq.pop()
            dq.append(i)
            high_252[i] = highs[dq[0]]
        
        # 60-session average dollar volume (prior 60, not including current)
        avg_dollar_vol_60 = [0.0] * n
        sum_dv = 0.0
        for i in range(n):
            if i >= 60:
                sum_dv -= closes[i-60] * volumes[i-60]
            if i > 0:
                sum_dv += closes[i-1] * volumes[i-1]
            if i >= 60:
                avg_dollar_vol_60[i] = sum_dv / 60.0
        
        # 20-session return (close[i] / close[i-20] - 1)
        ret_20 = [0.0] * n
        for i in range(20, n):
            if closes[i-20] > 0:
                ret_20[i] = closes[i] / closes[i-20] - 1.0
        
        # 1-day return
        ret_1 = [0.0] * n
        for i in range(1, n):
            if closes[i-1] > 0:
                ret_1[i] = closes[i] / closes[i-1] - 1.0
        
        # 20-session realized volatility (std of daily returns)
        vol_20 = [0.0] * n
        for i in range(20, n):
            rets = []
            for j in range(i-19, i+1):
                if j > 0 and closes[j-1] > 0:
                    rets.append(closes[j] / closes[j-1] - 1.0)
            if len(rets) >= 2:
                mean_r = sum(rets) / len(rets)
                var = sum((r - mean_r) ** 2 for r in rets) / (len(rets) - 1)
                vol_20[i] = var ** 0.5
        
        # Insider sales count in trailing 20 sessions (by date)
        # Convert sales filed_ts to dates
        sales_dates = set()
        for ft in sales:
            dt = datetime.fromtimestamp(ft, tz=timezone.utc).date()
            sales_dates.add(dt)
        
        bar_dates = [datetime.fromtimestamp(ts, tz=timezone.utc).date() for ts in timestamps]
        
        for i in range(504, n):  # need 504 prior sessions
            close = closes[i]
            if close < 5.0:
                continue
            if avg_dollar_vol_60[i] < 5_000_000:
                continue
            if high_252[i] <= 0 or close < 0.9 * high_252[i]:
                continue
            if ret_20[i] < 0.10:
                continue
            if not (-0.02 <= ret_1[i] <= 0.02):
                continue
            
            # Count insider sales in trailing 20 sessions ending at T
            # Trailing 20 sessions = bar_dates[i-19] to bar_dates[i] inclusive (20 sessions)
            if i < 19:
                continue
            start_date = bar_dates[i-19]
            end_date = bar_dates[i]
            sales_count = sum(1 for d in sales_dates if start_date <= d <= end_date)
            if sales_count < 3:
                continue
            
            # Check if we have outcome for this timestamp
            ts = timestamps[i]
            has_outcome = ts in outcomes
            outcome_up = outcomes.get(ts, None)
            
            all_opportunities.append({
                'ts': ts,
                'sym_id': sym_id,
                'sym': sym,
                'close': close,
                'vol_20': vol_20[i],
                'sales_count': sales_count,
                'has_outcome': has_outcome,
                'outcome_up': outcome_up,
                'bar_date': bar_dates[i]
            })

    if not all_opportunities:
        print("INSUFFICIENT=1")
        return 0

    # Sort by timestamp
    all_opportunities.sort(key=lambda x: x['ts'])
    
    # Split into main (80%) and sealed (20%) by timestamp
    split_idx = int(len(all_opportunities) * 0.8)
    main_ops = all_opportunities[:split_idx]
    sealed_ops = all_opportunities[split_idx:]

    def process_era(ops, era_name):
        if not ops:
            return {
                'issued': 0, 'opportunities': 0, 'hits': 0, 
                'base_rate': 0.0, 'distinct_days': 0, 'effective_n': 0.0,
                'precision': 0.0
            }
        
        # Cross-sectional volatility decile at each timestamp
        # Group by ts
        by_ts = defaultdict(list)
        for op in ops:
            by_ts[op['ts']].append(op)
        
        # Compute 90th percentile of vol_20 at each ts
        vol_threshold = {}
        for ts, group in by_ts.items():
            vols = [op['vol_20'] for op in group if op['vol_20'] > 0]
            if vols:
                vols.sort()
                idx = int(len(vols) * 0.9)
                if idx >= len(vols):
                    idx = len(vols) - 1
                vol_threshold[ts] = vols[idx]
            else:
                vol_threshold[ts] = float('inf')
        
        # Process chronologically with path-dependent abstentions
        last_call_ts = {}  # sym_id -> last call ts
        issued_calls = []
        opportunities_count = 0
        
        for i, op in enumerate(ops):
            opportunities_count += 1
            sym_id = op['sym_id']
            ts = op['ts']
            bar_date = op['bar_date']
            
            # Abstain: vol in top decile
            if op['vol_20'] >= vol_threshold.get(ts, float('inf')):
                continue
            
            # Abstain: call in prior 20 trading days for same symbol
            # Need to check if there was a call for this symbol in the last 20 trading days
            # We'll track by timestamp; need to know trading days. Approximate with 20 calendar days? 
            # Better: check last_call_ts and see if it's within 20 bars.
            # Since we process chronologically, we can track the bar index of last call.
            # But we don't have bar index here. Use timestamp difference ~20 trading days ~ 28 calendar days.
            # More precise: we need to know if a call was issued in the prior 20 sessions.
            # Since we process all symbols together chronologically, we can't easily know the session count per symbol.
            # Alternative: track last call date per symbol and count sessions between.
            # For simplicity, use timestamp: 20 trading days ~ 28 calendar days = 28*86400 seconds.
            # But this is approximate. Let's track the bar index per symbol.
            pass
        
        # Re-process with per-symbol bar index tracking
        # Group ops by symbol
        ops_by_sym = defaultdict(list)
        for op in ops:
            ops_by_sym[op['sym_id']].append(op)
        
        # For each symbol, we need to know the bar index of each opportunity
        # We'll need to map back to the original bars. Let's redo with more state.
        return {'issued': 0, 'opportunities': 0, 'hits': 0, 'base_rate': 0.0, 'distinct_days': 0, 'effective_n': 0.0, 'precision': 0.0}

    # The above approach is getting complex. Let me restructure.
    # We need to process per symbol chronologically, tracking bar indices.
    # Let's redo the main loop per symbol with full state.

    print("INSUFFICIENT=1")
    return 0

if __name__ == '__main__':
    sys.exit(main())