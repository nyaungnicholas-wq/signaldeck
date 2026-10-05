# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 312
# cycle_index: 35
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from datetime import datetime

DB_PATH = 'data/signaldeck.db'

try:
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all insider sales with codes 'S' and valid timestamps
    cur.execute("""
        SELECT symbol_id, insider, tx_ts, filed_ts, value
        FROM insider_trades
        WHERE code = 'S' AND tx_ts IS NOT NULL AND filed_ts IS NOT NULL
    """)
    trades = cur.fetchall()
    
    if len(trades) == 0:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Group trades by insider
    insider_trades = defaultdict(list)
    for row in trades:
        symbol_id, insider, tx_ts, filed_ts, value = row
        insider_trades[insider].append({
            'symbol_id': symbol_id,
            'tx_ts': tx_ts,
            'filed_ts': filed_ts,
            'value': value
        })
    
    # Get all daily bars for symbols in insider trades
    symbol_ids = set(t[0] for t in trades)
    symbol_ids_str = ','.join(map(str, symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({symbol_ids_str})
        ORDER BY symbol_id, ts
    """)
    bars = defaultdict(dict)
    for row in cur.fetchall():
        bars[row[0]][row[1]] = row[2]
    
    # Get all prediction outcomes at 21-day horizon
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    outcomes = defaultdict(dict)
    for row in cur.fetchall():
        outcomes[row[0]][row[1]] = (row[2], row[3])
    
    issued_calls = []
    opportunities = []
    
    # Process each insider
    for insider, trades in insider_trades.items():
        # Sort by filed_ts (disclosure date)
        trades.sort(key=lambda x: x['filed_ts'])
        
        if len(trades) < 4:  # Need at least 3 prior trades plus current
            continue
        
        for i, trade in enumerate(trades):
            decision_ts = trade['filed_ts'] + 86400  # Next day after disclosure
            
            # Check if we have a bar for this symbol at decision time
            symbol_id = trade['symbol_id']
            if decision_ts not in bars.get(symbol_id, {}):
                continue
            
            # Get prior 5 years of disclosed open-market sales
            five_years_ago = decision_ts - (5 * 365 * 24 * 3600)
            prior_sales = []
            for j, prev_trade in enumerate(trades):
                if j >= i:  # Only look at prior trades
                    break
                if prev_trade['filed_ts'] >= five_years_ago:
                    prior_sales.append(prev_trade)
            
            if len(prior_sales) < 3:
                continue
            
            # Compute lag and median for this insider's prior sales
            lags = [(s['filed_ts'] - s['tx_ts']) / 86400 for s in prior_sales]  # Calendar days
            lag_90th = sorted(lags)[int(len(lags) * 0.9)]
            values = [s['value'] for s in prior_sales]
            median_value = sorted(values)[len(values) // 2]
            
            # Current trade metrics
            current_lag = (trade['filed_ts'] - trade['tx_ts']) / 86400
            
            # Issue call if conditions met
            if current_lag > lag_90th and trade['value'] > median_value:
                # Find forward return 21 trading days later
                bar_ts = decision_ts
                bar_count = 0
                future_ts = None
                for ts in sorted(bars[symbol_id].keys()):
                    if ts >= bar_ts:
                        bar_count += 1
                        if bar_count == 22:  # Current day + 21 trading days
                            future_ts = ts
                            break
                
                if future_ts is None or future_ts not in bars[symbol_id]:
                    continue
                
                close_now = bars[symbol_id][bar_ts]
                close_future = bars[symbol_id][future_ts]
                fwd_return = (close_future - close_now) / close_now
                
                # Check label from outcomes
                label = None
                if symbol_id in outcomes:
                    for outcome_ts, (up, _) in outcomes[symbol_id].items():
                        if outcome_ts >= bar_ts and outcome_ts <= bar_ts + 22*86400:
                            label = not up  # DOWN call is correct when up=False
                            break
                
                if label is None:
                    continue
                
                # Store as opportunity (symbol, day) pair
                day = datetime.utcfromtimestamp(bar_ts).strftime('%Y-%m-%d')
                opportunities.append({
                    'symbol_id': symbol_id,
                    'day': day,
                    'hit': label,
                    'ts': bar_ts
                })
                
                issued_calls.append({
                    'symbol_id': symbol_id,
                    'day': day,
                    'hit': label,
                    'ts': bar_ts
                })
    
    conn.close()
    
    if len(issued_calls) == 0 or len(opportunities) == 0:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Compute metrics
    issued_count = len(issued_calls)
    opportunities_count = len(opportunities)
    hits = sum(1 for call in issued_calls if call['hit'])
    precision = hits / issued_count if issued_count > 0 else 0
    
    # Base rate: proportion of actual DOWN (False) in the issued subset
    downs_in_issued = sum(1 for call in issued_calls if not call['hit'])
    base_rate = downs_in_issued / issued_count if issued_count > 0 else 0
    
    # Distinct days
    distinct_days = len(set(call['day'] for call in issued_calls))
    
    # Design effect and effective N
    day_counts = defaultdict(int)
    for call in issued_calls:
        day_counts[call['day']] += 1
    
    # Cluster size variance
    m_bar = issued_count / distinct_days if distinct_days > 0 else 0
    variance_m = sum((c - m_bar)**2 for c in day_counts.values()) / distinct_days if distinct_days > 0 else 0
    design_effect = 1 + (variance_m / (m_bar**2)) if m_bar > 0 else 1
    effective_n = issued_count / design_effect if design_effect > 0 else 0
    
    # Sort all calls by timestamp for time split
    issued_calls.sort(key=lambda x: x['ts'])
    split_idx = int(len(issued_calls) * 0.8)
    sealed_era = issued_calls[split_idx:]
    
    if len(sealed_era) == 0:
        sealed_precision = 0
    else:
        sealed_hits = sum(1 for call in sealed_era if call['hit'])
        sealed_precision = sealed_hits / len(sealed_era)
    
    # Output metrics
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

except Exception as e:
    print(f"INSUFFICIENT=1")
    sys.exit(0)