# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 499
# cycle_index: 29
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    try:
        conn = sqlite3.connect(db_path, uri=True)
        conn.row_factory = sqlite3.Row
        c = conn.cursor()
        
        # Get daily bars with 252+ days history
        c.execute("""
            SELECT symbol_id, COUNT(*) as cnt
            FROM bars
            WHERE tf='1d'
            GROUP BY symbol_id
            HAVING cnt >= 252
        """)
        eligible_symbols = {row['symbol_id'] for row in c.fetchall()}
        
        # Get insider purchases (code P) with trade date and filing date
        c.execute("""
            SELECT symbol_id, tx_ts, filed_ts, shares, price
            FROM insider_trades
            WHERE code='P'
        """)
        trades = c.fetchall()
        
        if not trades:
            print("INSUFFICIENT=1")
            return
            
        # Precompute 252-day range stats for each eligible symbol
        range_stats = {}
        for sym in eligible_symbols:
            c.execute("""
                SELECT MIN(low) as min_low, MAX(high) as max_high
                FROM (
                    SELECT low, high
                    FROM bars
                    WHERE symbol_id=? AND tf='1d'
                    ORDER BY ts DESC
                    LIMIT 252
                )
            """, (sym,))
            row = c.fetchone()
            if row and row['min_low'] and row['max_high']:
                range_stats[sym] = (row['min_low'], row['max_high'])
        
        # Process each qualifying trade
        signals = []
        opportunities = 0
        
        for trade in trades:
            sym = trade['symbol_id']
            trade_ts = trade['tx_ts']
            filed_ts = trade['filed_ts']
            
            if sym not in eligible_symbols or sym not in range_stats:
                continue
                
            opportunities += 1
            
            # Convert timestamps to dates
            trade_date = datetime.utcfromtimestamp(trade_ts).date()
            filed_date = datetime.utcfromtimestamp(filed_ts).date()
            
            # Disclosure lag in trading days
            disclosure_lag = 0
            current = trade_date
            while current < filed_date:
                if current.weekday() < 5:  # Mon-Fri
                    disclosure_lag += 1
                current += timedelta(days=1)
            
            if disclosure_lag > 10:
                continue
                
            # Get trade date close price
            c.execute("""
                SELECT close
                FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts=?
            """, (sym, trade_ts))
            row = c.fetchone()
            if not row:
                continue
            trade_close = row['close']
            
            # Check tercile condition
            min_low, max_high = range_stats[sym]
            tercile_width = (max_high - min_low) / 3
            low_tercile = min_low + tercile_width
            high_tercile = min_low + 2 * tercile_width
            
            if not (low_tercile <= trade_close <= high_tercile):
                continue
                
            # Find reference date (first trading day after disclosure)
            c.execute("""
                SELECT MIN(ts) as ref_ts
                FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts > ?
            """, (sym, filed_ts))
            row = c.fetchone()
            if not row or not row['ref_ts']:
                continue
            ref_ts = row['ref_ts']
            
            # Find horizon date (21 trading days after reference)
            c.execute("""
                SELECT ts
                FROM (
                    SELECT ts
                    FROM bars
                    WHERE symbol_id=? AND tf='1d' AND ts > ?
                    ORDER BY ts
                    LIMIT 21
                )
                ORDER BY ts DESC
                LIMIT 1
            """, (sym, ref_ts))
            row = c.fetchone()
            if not row:
                continue
            horizon_ts = row['ts']
            
            # Get reference and horizon closes
            c.execute("""
                SELECT close FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts=?
            """, (sym, ref_ts))
            ref_close = c.fetchone()['close']
            
            c.execute("""
                SELECT close FROM bars
                WHERE symbol_id=? AND tf='1d' AND ts=?
            """, (sym, horizon_ts))
            horizon_close = c.fetchone()['close']
            
            # Determine direction
            up = 1 if horizon_close > ref_close else 0
            
            signals.append({
                'symbol_id': sym,
                'ref_date': datetime.utcfromtimestamp(ref_ts).date(),
                'up': up
            })
        
        if not signals:
            print("INSUFFICIENT=1")
            return
            
        # Sort by reference date and split into train/test (last 20%)
        signals.sort(key=lambda x: x['ref_date'])
        test_size = max(1, len(signals) // 5)
        train = signals[:-test_size]
        test = signals[-test_size:]
        
        # Compute metrics
        issued = len(signals)
        hits = sum(1 for s in signals if s['up'] == 1)
        precision = hits / issued if issued else 0
        base_rate = hits / issued  # base rate within issued
        
        # Distinct days
        distinct_days = len(set(s['ref_date'] for s in signals))
        
        # Design effect calculation
        day_counts = defaultdict(int)
        for s in signals:
            day_counts[s['ref_date']] += 1
        
        mean_per_day = issued / distinct_days
        variance = sum((c - mean_per_day)**2 for c in day_counts.values()) / distinct_days
        design_effect = 1 + (variance / mean_per_day) if mean_per_day else 1
        effective_n = issued / design_effect if design_effect else issued
        
        # Sealed era (test set)
        sealed_hits = sum(1 for s in test if s['up'] == 1)
        sealed_precision = sealed_hits / len(test) if test else 0
        
        # Print required metrics
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()