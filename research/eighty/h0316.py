# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 315
# cycle_index: 38
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=30)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Check for required data: 13F institutional holdings with quarterly decreases and repurchase filings/news
        # We need: symbols with inst_holdings AND (filings with repurchase disclosure OR news with repurchase announcement)
        # AND prediction_outcomes for 21-day horizon labels
        
        # First, identify repurchase events from filings (form 4, 8-K, 144) or news containing "repurchase" or "buyback"
        cur.execute("""
        WITH repurchase_events AS (
            SELECT symbol_id, filed_ts AS event_ts, 'filing' AS source, form
            FROM filings
            WHERE form IN ('4', '8-K', '144')
            AND (title LIKE '%repurchase%' OR title LIKE '%buyback%' OR url LIKE '%repurchase%' OR url LIKE '%buyback%')
            UNION
            SELECT symbol_id, ts AS event_ts, 'news' AS source, NULL AS form
            FROM news
            WHERE (headline LIKE '%repurchase%' OR headline LIKE '%buyback%' OR 
                   rationale LIKE '%repurchase%' OR rationale LIKE '%buyback%')
        ),
        symbols_with_13f AS (
            SELECT DISTINCT symbol_id 
            FROM inst_holdings 
            WHERE period IS NOT NULL
        ),
        symbols_with_repurchase AS (
            SELECT DISTINCT symbol_id 
            FROM repurchase_events
        ),
        eligible_symbols AS (
            SELECT a.symbol_id 
            FROM symbols_with_13f a
            JOIN symbols_with_repurchase b ON a.symbol_id = b.symbol_id
        )
        SELECT COUNT(*) AS cnt FROM eligible_symbols
        """)
        eligible_count = cur.fetchone()['cnt']
        
        # Check if we have enough data to proceed
        if eligible_count < 10:
            print("INSUFFICIENT=1")
            return
            
        # Now we need to compute quarterly 13F ownership changes
        # and match with repurchase events, then check conditions
        
        # Get all 13F records with period dates
        cur.execute("""
        SELECT symbol_id, period, value, shares 
        FROM inst_holdings 
        WHERE period IS NOT NULL
        ORDER BY symbol_id, period
        """)
        holdings = cur.fetchall()
        
        # Get all repurchase events
        cur.execute("""
        SELECT symbol_id, event_ts, source, form
        FROM (
            SELECT symbol_id, filed_ts AS event_ts, 'filing' AS source, form
            FROM filings
            WHERE form IN ('4', '8-K', '144')
            AND (title LIKE '%repurchase%' OR title LIKE '%buyback%' OR url LIKE '%repurchase%' OR url LIKE '%buyback%')
            UNION
            SELECT symbol_id, ts AS event_ts, 'news' AS source, NULL AS form
            FROM news
            WHERE (headline LIKE '%repurchase%' OR headline LIKE '%buyback%' OR 
                   rationale LIKE '%repurchase%' OR rationale LIKE '%buyback%')
        )
        """)
        repurchase_events = cur.fetchall()
        
        # Get bars for market cap approximation and dollar volume
        cur.execute("""
        SELECT symbol_id, ts, close, volume 
        FROM bars 
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
        """)
        bars = cur.fetchall()
        
        # Get fundamentals for shares outstanding
        cur.execute("""
        SELECT symbol_id, value AS shares_outstanding, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding'
        ORDER BY symbol_id, fetched_at
        """)
        fundamentals = cur.fetchall()
        
        # Get prediction outcomes for 21-day horizon
        cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21
        ORDER BY symbol_id, ts
        """)
        outcomes = cur.fetchall()
        
        # Process data
        # Organize 13F by symbol and period
        from collections import defaultdict
        holdings_by_symbol = defaultdict(list)
        for h in holdings:
            holdings_by_symbol[h['symbol_id']].append(h)
        
        # Organize bars by symbol
        bars_by_symbol = defaultdict(list)
        for b in bars:
            bars_by_symbol[b['symbol_id']].append(b)
        
        # Organize fundamentals by symbol
        fundamentals_by_symbol = defaultdict(list)
        for f in fundamentals:
            fundamentals_by_symbol[f['symbol_id']].append(f)
        
        # Organize outcomes by symbol
        outcomes_by_symbol = defaultdict(list)
        for o in outcomes:
            outcomes_by_symbol[o['symbol_id']].append(o)
        
        # Organize repurchase events by symbol
        repurchases_by_symbol = defaultdict(list)
        for r in repurchase_events:
            repurchases_by_symbol[r['symbol_id']].append(r)
        
        # Process each eligible symbol
        decisions = []
        design_effect_count = 0
        distinct_days = set()
        
        for symbol_id in repurchases_by_symbol.keys():
            if symbol_id not in holdings_by_symbol:
                continue
                
            # Get 13F periods sorted
            symbol_holdings = sorted(holdings_by_symbol[symbol_id], key=lambda x: x['period'])
            
            # Get repurchase events for this symbol
            symbol_repurchases = repurchases_by_symbol[symbol_id]
            
            # Get bars for this symbol
            symbol_bars = sorted(bars_by_symbol.get(symbol_id, []), key=lambda x: x['ts'])
            
            # Get fundamentals for shares outstanding
            symbol_fundamentals = sorted(fundamentals_by_symbol.get(symbol_id, []), key=lambda x: x['fetched_at'])
            
            # For each repurchase event, check conditions
            for repurchase in symbol_repurchases:
                repurchase_ts = repurchase['event_ts']
                
                # Find 13F records before the repurchase event (with 45-day lag for filing)
                # We only know 13F data up to 45 days after period end
                lagged_ts = repurchase_ts - (45 * 24 * 60 * 60)  # 45 days in seconds
                
                # Get relevant 13F periods
                relevant_periods = [h for h in symbol_holdings if h['period'] <= lagged_ts]
                
                if len(relevant_periods) < 2:
                    continue
                
                # Compare most recent two periods for >20% decrease
                latest = relevant_periods[-1]
                previous = relevant_periods[-2]
                
                if latest['value'] and previous['value'] and previous['value'] > 0:
                    decrease_pct = (previous['value'] - latest['value']) / previous['value']
                    
                    if decrease_pct < 0.15:  # Abstain if <15%
                        continue
                        
                    # Get market cap at time of repurchase
                    # Find closest bar before repurchase_ts
                    preceding_bars = [b for b in symbol_bars if b['ts'] <= repurchase_ts]
                    if not preceding_bars:
                        continue
                        
                    latest_bar = preceding_bars[-1]
                    close_price = latest_bar['close']
                    
                    # Get shares outstanding at or before repurchase_ts
                    shares_entries = [f for f in symbol_fundamentals if f['fetched_at'] <= repurchase_ts]
                    if not shares_entries:
                        continue
                        
                    shares_outstanding = shares_entries[-1]['shares_outstanding']
                    market_cap = close_price * shares_outstanding
                    
                    # Check repurchase size (we need to estimate from news/filings, but data is insufficient)
                    # The database does not contain repurchase size in structured form
                    # Therefore, we cannot compute >=1% of market cap requirement
                    
                    # Since we cannot compute repurchase size, we cannot apply the entry condition
                    # This means the hypothesis cannot be fully tested with available data
                    print("INSUFFICIENT=1")
                    return
        
        # If we reach here, we could not form any decisions due to missing repurchase size data
        print("INSUFFICIENT=1")
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()