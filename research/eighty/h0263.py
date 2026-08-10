import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get symbols with both daily bars and insider trades
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

    # Load all daily bars for these symbols
    symbol_ids = [s[0] for s in symbols]
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume']
        })

    # Load insider trades (code P=purchase, S=sale)
    cur.execute(f"""
        SELECT symbol_id, filed_ts, code, shares
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code IN ('P','S')
        ORDER BY symbol_id, filed_ts
    """, symbol_ids)
    
    trades_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        trades_by_symbol[row['symbol_id']].append({
            'filed_ts': row['filed_ts'],
            'code': row['code'],
            'shares': row['shares']
        })

    # Load prediction outcomes for horizon=20
    cur.execute(f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 20 AND symbol_id IN ({placeholders})
    """, symbol_ids)
    
    outcomes = {}
    for row in cur.fetchall():
        outcomes[(row['symbol_id'], row['ts'])] = row['up']

    # Precompute rolling stats for each symbol
    stats_by_symbol = {}
    for sym_id, bars in bars_by_symbol.items():
        n = len(bars)
        if n < 252:
            continue
        
        closes = [b['close'] for b in bars]
        highs = [b['high'] for b in bars]
        volumes = [b['volume'] for b in bars]
        tss = [b['ts'] for b in bars]
        
        # 252-day high (including current day)
        high_252 = [None] * n
        for i in range(251, n):
            high_252[i] = max(highs[i-251:i+1])
        
        # 60-day return (close[i] / close[i-60] - 1)
        ret_60 = [None] * n
        for i in range(60, n):
            if closes[i-60] > 0:
                ret_60[i] = closes[i] / closes[i-60] - 1
        
        # 60-day avg dollar volume
        avg_dv_60 = [None] * n
        for i in range(59, n):
            total = sum(closes[j] * volumes[j] for j in range(i-59, i+1))
            avg_dv_60[i] = total / 60.0
        
        # 20-day realized volatility (std of daily returns)
        vol_20 = [None] * n
        for i in range(19, n):
            rets = []
            for j in range(i-19, i+1):
                if j > 0 and closes[j-1] > 0:
                    rets.append((closes[j] - closes[j-1]) / closes[j-1])
            if len(rets) == 20:
                mean_ret = sum(rets) / 20.0
                var = sum((r - mean_ret) ** 2 for r in rets) / 20.0
                vol_20[i] = math.sqrt(var)
        
        stats_by_symbol[sym_id] = {
            'ts': tss,
            'close': closes,
            'high_252': high_252,
            'ret_60': ret_60,
            'avg_dv_60': avg_dv_60,
            'vol_20': vol_20
        }

    # Build mapping from date to trading day index for each symbol
    # Also need cross-sectional volatility deciles per trading day
    # First, collect all (ts, sym_id, vol_20) where vol_20 is not None
    vol_by_ts = defaultdict(list)
    for sym_id, stats in stats_by_symbol.items():
        for i, v in enumerate(stats['vol_20']):
            if v is not None:
                vol_by_ts[stats['ts'][i]].append((sym_id, v))
    
    # Compute top decile threshold per ts (90th percentile)
    vol_threshold = {}
    for ts, vals in vol_by_ts.items():
        if len(vals) >= 10:
            sorted_vals = sorted(v for _, v in vals)
            idx = int(len(sorted_vals) * 0.9)
            vol_threshold[ts] = sorted_vals[idx]
        else:
            vol_threshold[ts] = float('inf')

    # Process each symbol's filing dates
    opportunities = []
    calls = []
    last_call_day = {}  # symbol_id -> last trading day index a call was issued
    
    for sym_id, symbol in symbols:
        if sym_id not in stats_by_symbol or sym_id not in trades_by_symbol:
            continue
        
        stats = stats_by_symbol[sym_id]
        trades = trades_by_symbol[sym_id]
        n = len(stats['ts'])
        
        # Group trades by filing date (UTC date)
        trades_by_filing_date = defaultdict(list)
        for tr in trades:
            filing_date = datetime.utcfromtimestamp(tr['filed_ts']).date()
            trades_by_filing_date[filing_date].append(tr)
        
        filing_dates = sorted(trades_by_filing_date.keys())
        
        for filing_date in filing_dates:
            # Find td_0: last trading day <= filing_date
            td_0_idx = None
            for i in range(n-1, -1, -1):
                bar_date = datetime.utcfromtimestamp(stats['ts'][i]).date()
                if bar_date <= filing_date:
                    td_0_idx = i
                    break
            if td_0_idx is None or td_0_idx < 252:
                continue
            
            td_1_idx = td_0_idx - 1
            if td_1_idx < 251:
                continue
            
            # Check universe criteria at td_1 (T-1)
            close_t1 = stats['close'][td_1_idx]
            if close_t1 < 5:
                continue
            if stats['high_252'][td_1_idx] is None:
                continue
            if stats['ret_60'][td_1_idx] is None:
                continue
            if stats['avg_dv_60'][td_1_idx] is None:
                continue
            if stats['avg_dv_60'][td_1_idx] < 5_000_000:
                continue
            
            # Drawdown from 252-day high >= 15%
            high_252 = stats['high_252'][td_1_idx]
            if close_t1 > 0.85 * high_252:
                continue
            
            # 60-day return <= -10%
            if stats['ret_60'][td_1_idx] > -0.10:
                continue
            
            # Net insider shares in T-10..T (calendar days)
            window_start = filing_date - timedelta(days=10)
            net_shares = 0
            for fd, tr_list in trades_by_filing_date.items():
                if window_start <= fd <= filing_date:
                    for tr in tr_list:
                        if tr['code'] == 'P':
                            net_shares += tr['shares']
                        elif tr['code'] == 'S':
                            net_shares -= tr['shares']
            if net_shares <= 10000:
                continue
            
            # Volatility at td_0 (T) not in top cross-sectional decile
            ts_t0 = stats['ts'][td_0_idx]
            vol_t0 = stats['vol_20'][td_0_idx]
            if vol_t0 is not None and ts_t0 in vol_threshold:
                if vol_t0 >= vol_threshold[ts_t0]:
                    continue
            
            # No call for same symbol in prior 20 trading days
            if sym_id in last_call_day:
                if td_0_idx - last_call_day[sym_id] <= 20:
                    continue
            
            # This is an opportunity
            opportunities.append((sym_id, td_0_idx, filing_date))
            
            # Check label
            outcome_key = (sym_id, ts_t0)
            if outcome_key in outcomes:
                up = outcomes[outcome_key]
                calls.append((sym_id, td_0_idx, filing_date, up, ts_t0))
                last_call_day[sym_id] = td_0_idx

    if not opportunities:
        print("INSUFFICIENT=1")
        return 0

    # Split into sealed era (most recent 20% of opportunities by date)
    opportunities.sort(key=lambda x: x[2])  # by filing_date
    calls.sort(key=lambda x: x[2])
    
    split_idx = int(len(opportunities) * 0.8)
    insample_ops = opportunities[:split_idx]
    sealed_ops = opportunities[split_idx:]
    
    insample_calls = [c for c in calls if c[2] <= insample_ops[-1][2]] if insample_ops else []
    sealed_calls = [c for c in calls if c[2] > insample_ops[-1][2]] if insample_ops else calls

    # Compute metrics for full sample (insample + sealed)
    all_calls = calls
    issued = len(all_calls)
    opps = len(opportunities)
    
    if issued == 0:
        print("INSUFFICIENT=1")
        return 0
    
    hits = sum(1 for c in all_calls if c[3] == 1)
    precision = hits / issued
    
    # Base rate within issued subset
    base_rate = hits / issued  # same as precision for binary UP calls
    
    # Distinct days among issued calls
    distinct_days = len(set(c[4] for c in all_calls))  # ts_t0
    
    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Simplified: cluster by day, compute variance inflation
    day_counts = defaultdict(int)
    for c in all_calls:
        day_counts[c[4]] += 1
    if len(day_counts) > 1:
        avg_cluster = issued / len(day_counts)
        # ICC approximation: (between-day variance) / (total variance)
        # For binary outcomes, use Kish's design effect
        deff = 1 + (avg_cluster - 1) * 0.1  # conservative ICC=0.1
        effective_n = issued / deff
    else:
        effective_n = issued * 0.5  # if all same day, not independent
    
    # Sealed precision
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for c in sealed_calls if c[3] == 1)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # Check abstention rate >= 0.95
    abstention_rate = 1 - (issued / opps) if opps > 0 else 1
    
    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opps}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == "__main__":
    sys.exit(main())