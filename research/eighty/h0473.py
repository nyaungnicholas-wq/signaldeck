# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 472
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check available horizons in prediction_outcomes
    cur.execute("SELECT DISTINCT horizon FROM prediction_outcomes ORDER BY horizon")
    horizons = [row['horizon'] for row in cur.fetchall()]
    if not horizons:
        print("INSUFFICIENT=1")
        return 0

    # Pick a 6-month-ish horizon
    target_horizon = None
    for h in horizons:
        h_lower = h.lower()
        if any(x in h_lower for x in ['6m', '180', '126', 'half']):
            target_horizon = h
            break
    if not target_horizon:
        target_horizon = horizons[0]

    # Get all insider purchases (code='P') with director title, filed_ts >= 2018-01-01 (bars start)
    # Title contains 'Director' but not officer titles
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code, it.shares, it.price, 
               it.value, it.tx_ts, it.filed_ts
        FROM insider_trades it
        WHERE it.code = 'P'
          AND it.filed_ts >= 1514764800  -- 2018-01-01
          AND (it.title LIKE '%Director%' OR it.title LIKE '%director%')
          AND it.title NOT LIKE '%CEO%'
          AND it.title NOT LIKE '%CFO%'
          AND it.title NOT LIKE '%President%'
          AND it.title NOT LIKE '%Chief%'
          AND it.title NOT LIKE '%COO%'
          AND it.title NOT LIKE '%CTO%'
          AND it.title NOT LIKE '%CIO%'
        ORDER BY it.filed_ts
    """)
    insider_trades = cur.fetchall()
    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0

    # For each trade, check if first purchase in 730 days for this insider
    # We'll do this in Python for flexibility
    symbol_ids = set(t['symbol_id'] for t in insider_trades)
    placeholders = ','.join('?' * len(symbol_ids))
    
    # Get bars for these symbols to compute 52-week highs
    # We need daily bars (tf='1d') for price history
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(symbol_ids))
    bars_rows = cur.fetchall()

    # Organize bars by symbol_id
    bars_by_symbol = {}
    for row in bars_rows:
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append((row['ts'], row['close']))

    # Get prediction_outcomes for target horizon
    cur.execute("""
        SELECT symbol_id, horizon, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = ?
        ORDER BY symbol_id, ts
    """, (target_horizon,))
    outcomes_rows = cur.fetchall()

    # Organize outcomes by symbol_id
    outcomes_by_symbol = {}
    for row in outcomes_rows:
        sid = row['symbol_id']
        if sid not in outcomes_by_symbol:
            outcomes_by_symbol[sid] = []
        outcomes_by_symbol[sid].append((row['ts'], row['up'], row['fwd_return']))

    # Process each insider trade
    issued = []
    opportunities = 0
    
    # Track insider purchase history for "first in 730 days"
    insider_last_purchase = {}  # (insider, symbol_id) -> last tx_ts
    
    for trade in insider_trades:
        opportunities += 1
        symbol_id = trade['symbol_id']
        insider = trade['insider']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']
        trade_price = trade['price']
        
        # Check disclosure delay <= 2 days (172800 seconds)
        if filed_ts - tx_ts > 172800:
            continue
            
        # Check first purchase in 730 days (63072000 seconds) for this insider+symbol
        key = (insider, symbol_id)
        last_tx = insider_last_purchase.get(key, 0)
        if last_tx > 0 and tx_ts - last_tx < 63072000:
            continue
        insider_last_purchase[key] = tx_ts
        
        # Get 52-week high as of tx_ts from bars
        bars = bars_by_symbol.get(symbol_id, [])
        if not bars:
            continue
            
        # Find bars up to tx_ts (tx_ts is unix epoch, bars.ts is unix epoch)
        # 52 weeks ~ 365 days ~ 31536000 seconds
        window_start = tx_ts - 31536000
        relevant_bars = [b for b in bars if window_start <= b[0] <= tx_ts]
        if len(relevant_bars) < 10:  # Need sufficient history
            continue
            
        high_52w = max(b[1] for b in relevant_bars)
        if high_52w <= 0:
            continue
            
        # Check if trade price >30% below 52-week high
        drawdown = (high_52w - trade_price) / high_52w
        if drawdown < 0.30:
            continue
            
        # Find matching prediction_outcome: closest ts <= filed_ts (or nearest)
        outcomes = outcomes_by_symbol.get(symbol_id, [])
        if not outcomes:
            continue
            
        # Find outcome with ts closest to filed_ts (decision time)
        best_outcome = min(outcomes, key=lambda o: abs(o[0] - filed_ts))
        outcome_ts, up, fwd_return = best_outcome
        
        # Require outcome_ts within 5 days of filed_ts (432000 seconds)
        if abs(outcome_ts - filed_ts) > 432000:
            continue
            
        # Convert filed_ts to UTC date for distinct day counting
        filed_date = datetime.utcfromtimestamp(filed_ts).date()
        
        issued.append({
            'symbol_id': symbol_id,
            'filed_ts': filed_ts,
            'filed_date': filed_date,
            'up': up,
            'fwd_return': fwd_return,
            'drawdown': drawdown,
            'disclosure_delay': filed_ts - tx_ts
        })

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    # Sort by filed_ts
    issued.sort(key=lambda x: x['filed_ts'])
    
    # Hold out most recent 20% as sealed era
    n_total = len(issued)
    n_sealed = max(1, int(n_total * 0.2))
    train_issued = issued[:-n_sealed]
    sealed_issued = issued[-n_sealed:]
    
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0, 0
        n = len(calls)
        hits = sum(1 for c in calls if c['up'] == 1)
        precision = hits / n
        base_rate = precision  # Within issued subset, base rate of predicted class (up=1)
        distinct_days = len(set(c['filed_date'] for c in calls))
        # Design effect: clustering by day
        design_effect = max(1.01, n / distinct_days) if distinct_days > 0 else 1.01
        effective_n = n / design_effect
        return n, hits, precision, base_rate, distinct_days, effective_n

    train_n, train_hits, train_precision, train_base_rate, train_distinct_days, train_effective_n = compute_metrics(train_issued)
    sealed_n, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_issued)
    
    total_n = len(issued)
    total_hits = sum(1 for c in issued if c['up'] == 1)
    total_precision = total_hits / total_n
    total_base_rate = total_precision
    total_distinct_days = len(set(c['filed_date'] for c in issued))
    total_design_effect = max(1.01, total_n / total_distinct_days) if total_distinct_days > 0 else 1.01
    total_effective_n = total_n / total_design_effect

    # Print required lines
    print(f"ISSUED={total_n}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base_rate:.6f}")
    print(f"DISTINCT_DAYS={total_distinct_days}")
    print(f"EFFECTIVE_N={total_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())