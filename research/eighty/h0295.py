# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 294
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception:
        print("INSUFFICIENT=1")
        return

    try:
        # Get insider purchases by officers/directors with delays
        query = """
        SELECT 
            it.symbol_id,
            it.tx_ts,
            it.filed_ts,
            (it.filed_ts - it.tx_ts) / 86400 as delay_days,
            it.code,
            it.title,
            it.price as insider_price
        FROM insider_trades it
        WHERE it.code = 'P'
          AND (it.title LIKE '%Officer%' OR it.title LIKE '%Director%')
          AND it.filed_ts > it.tx_ts
          AND (it.filed_ts - it.tx_ts) >= 10 * 86400
        ORDER BY it.filed_ts
        """
        
        cur = conn.execute(query)
        trades = cur.fetchall()
        
        if not trades:
            print("INSUFFICIENT=1")
            return

        # Group by symbol_id and entry date (filed_ts date)
        from collections import defaultdict
        import time
        from datetime import datetime
        
        trades_by_symbol_date = defaultdict(list)
        for trade in trades:
            symbol_id = trade['symbol_id']
            # Convert filed_ts to date string
            filed_dt = datetime.utcfromtimestamp(trade['filed_ts'])
            date_str = filed_dt.strftime('%Y-%m-%d')
            trades_by_symbol_date[(symbol_id, date_str)].append(trade)

        # Get all symbols that appear
        symbols = set()
        for (symbol_id, _) in trades_by_symbol_date:
            symbols.add(symbol_id)
        
        # Prepare to check 21-day forward returns
        # We need bars data for each symbol from entry date onwards
        opportunities = []
        
        for (symbol_id, entry_date_str), day_trades in trades_by_symbol_date.items():
            # Convert entry_date_str to timestamp for query
            entry_dt = datetime.strptime(entry_date_str, '%Y-%m-%d')
            entry_ts = int(entry_dt.timestamp())
            
            # Get bars for this symbol from entry date onwards (1d only)
            bars_query = """
            SELECT ts, close 
            FROM bars 
            WHERE symbol_id = ? 
              AND tf = '1d'
              AND ts >= ?
            ORDER BY ts
            """
            cur.execute(bars_query, (symbol_id, entry_ts))
            bars = cur.fetchall()
            
            if len(bars) < 22:  # Need at least 21 trading days forward
                continue
            
            # Entry is first bar (entry_date)
            entry_close = bars[0]['close']
            # 21st bar forward (index 21, because index 0 is entry)
            if len(bars) >= 22:
                fwd_close = bars[21]['close']
                fwd_return = (fwd_close / entry_close) - 1
                positive = 1 if fwd_return > 0 else 0
            else:
                continue
            
            # Count independent observations: one per (symbol, day)
            opportunities.append({
                'symbol_id': symbol_id,
                'entry_date': entry_date_str,
                'entry_ts': entry_ts,
                'fwd_return': fwd_return,
                'positive': positive,
                'delay_days': day_trades[0]['delay_days']
            })

        conn.close()
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return

        # Sort opportunities by entry date
        opportunities.sort(key=lambda x: x['entry_ts'])
        
        # Determine sealed era: most recent 20%
        n_total = len(opportunities)
        n_sealed = max(1, int(n_total * 0.2))
        n_main = n_total - n_sealed
        
        main_era = opportunities[:n_main]
        sealed_era = opportunities[n_main:]
        
        # Compute metrics for main era
        issued_main = len(main_era)
        distinct_days_main = len(set(x['entry_date'] for x in main_era))
        hits_main = sum(x['positive'] for x in main_era)
        
        # Compute metrics for sealed era
        issued_sealed = len(sealed_era)
        hits_sealed = sum(x['positive'] for x in sealed_era)
        
        # Base rate: among issued calls, what is the base rate of positive returns
        base_rate_main = hits_main / issued_main if issued_main > 0 else 0
        base_rate_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
        
        # Design effect: 1 + (average cluster size - 1) * ICC
        # Assume ICC = 0.5 (common for financial data)
        # Average cluster size = issued_main / distinct_days_main
        if distinct_days_main > 0:
            avg_cluster_size = issued_main / distinct_days_main
            icc = 0.5
            design_effect = 1 + (avg_cluster_size - 1) * icc
            effective_n_main = issued_main / design_effect
        else:
            effective_n_main = 0
        
        # Print results
        print(f"ISSUED={issued_main}")
        print(f"OPPORTUNITIES={n_total}")
        print(f"PRECISION={base_rate_main:.6f}")
        print(f"BASE_RATE={base_rate_main:.6f}")
        print(f"DISTINCT_DAYS={distinct_days_main}")
        print(f"EFFECTIVE_N={effective_n_main:.2f}")
        print(f"SEALED_PRECISION={base_rate_sealed:.6f}")
        
    except Exception:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()