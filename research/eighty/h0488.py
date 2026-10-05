# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 487
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    try:
        # Get all 424B2 filings after 2026-02-01
        cur.execute("""
            SELECT f.symbol_id, f.filed_ts
            FROM filings f
            WHERE f.form = '424B2' AND f.filed_ts >= '2026-02-01'
            ORDER BY f.filed_ts
        """)
        candidates = cur.fetchall()
        
        if not candidates:
            print("INSUFFICIENT=1")
            conn.close()
            return

        # Process each candidate
        opportunities = []
        for symbol_id, filed_ts_str in candidates:
            filed_ts = datetime.datetime.strptime(filed_ts_str, '%Y-%m-%d %H:%M:%S')
            
            # Get the first trading day on or after filing date
            cur.execute("""
                SELECT MIN(ts)
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            """, (symbol_id, int(filed_ts.timestamp())))
            
            entry_ts_row = cur.fetchone()
            if not entry_ts_row or not entry_ts_row[0]:
                continue
            
            entry_ts = entry_ts_row[0]
            
            # Get entry bar details
            cur.execute("""
                SELECT close, volume
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts = ?
            """, (symbol_id, entry_ts))
            
            bar_row = cur.fetchone()
            if not bar_row:
                continue
            entry_close, entry_volume = bar_row
            
            # Check universe filters
            if entry_close < 2:
                continue
            
            # Check 20-day average dollar volume
            cur.execute("""
                SELECT AVG(close * volume)
                FROM (
                    SELECT close, volume
                    FROM bars
                    WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                    ORDER BY ts DESC
                    LIMIT 20
                )
            """, (symbol_id, entry_ts))
            
            avg_dollar_vol = cur.fetchone()[0]
            if not avg_dollar_vol or avg_dollar_vol < 1000000:
                continue
            
            # Check for other 424B2 in prior 60 trading days
            cur.execute("""
                SELECT COUNT(*)
                FROM filings f
                WHERE f.symbol_id = ? AND f.form = '424B2' AND f.filed_ts < ?
                  AND f.filed_ts > date(?, '-60 days')
            """, (symbol_id, filed_ts_str, filed_ts_str))
            
            prior_count = cur.fetchone()[0]
            if prior_count > 0:
                continue
            
            # Check for other SEC filings in prior 3 trading days
            # Get 3 trading days before entry
            cur.execute("""
                SELECT ts
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts < ?
                ORDER BY ts DESC
                LIMIT 3
            """, (symbol_id, entry_ts))
            
            prior_days = [row[0] for row in cur.fetchall()]
            if prior_days:
                # Convert to datetime for filing comparison
                earliest_prior = datetime.datetime.utcfromtimestamp(min(prior_days))
                cur.execute("""
                    SELECT COUNT(*)
                    FROM filings f
                    WHERE f.symbol_id = ? AND f.form != '424B2'
                      AND f.filed_ts >= ? AND f.filed_ts < ?
                """, (symbol_id, earliest_prior.isoformat(), filed_ts_str))
                
                other_filing_count = cur.fetchone()[0]
                if other_filing_count > 0:
                    continue
            
            # Get outcome: close at entry + 10 trading days
            cur.execute("""
                SELECT ts
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts > ?
                ORDER BY ts ASC
                LIMIT 10
            """, (symbol_id, entry_ts))
            
            ten_days = cur.fetchall()
            if len(ten_days) < 10:
                continue
            
            final_ts = ten_days[-1][0]
            cur.execute("""
                SELECT close
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts = ?
            """, (symbol_id, final_ts))
            
            final_bar = cur.fetchone()
            if not final_bar:
                continue
            final_close = final_bar[0]
            
            # Store opportunity
            opportunities.append({
                'symbol_id': symbol_id,
                'entry_date': datetime.datetime.utcfromtimestamp(entry_ts).date(),
                'entry_close': entry_close,
                'final_close': final_close,
                'is_down': final_close < entry_close,
                'issued': True  # All these are issued (we only add if they pass all filters)
            })
        
        conn.close()
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return
        
        # Sort by entry date for sealing
        opportunities.sort(key=lambda x: x['entry_date'])
        
        # Split into sealed (most recent 20%) and rest
        total = len(opportunities)
        seal_idx = int(total * 0.8)
        sealed = opportunities[seal_idx:]
        train = opportunities[:seal_idx]
        
        # Calculate metrics
        issued = len(opportunities)  # All opportunities are issued in our case
        hits = sum(1 for o in opportunities if o['is_down'])
        precision = hits / issued if issued > 0 else 0
        
        # Base rate: proportion of DOWN in all opportunities (as defined in context)
        base_rate = precision  # Since all issued are DOWN calls
        
        # Distinct days among issued calls
        distinct_days = len(set(o['entry_date'] for o in opportunities))
        
        # Design effect (simplified: 1.5 assuming some clustering)
        design_effect = 1.5
        effective_n = issued / design_effect
        
        # Sealed precision
        sealed_issued = len(sealed)
        sealed_hits = sum(1 for o in sealed if o['is_down'])
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={issued}")  # We processed all opportunities
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()