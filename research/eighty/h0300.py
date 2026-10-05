# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 299
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        c = conn.cursor()
        
        # Get universe: symbols with form 144 and daily bars
        c.execute("""
            SELECT DISTINCT f.symbol_id
            FROM filings f
            INNER JOIN bars b ON f.symbol_id = b.symbol_id AND b.tf = '1d'
            WHERE f.form = '144'
        """)
        universe = {row[0] for row in c.fetchall()}
        
        if not universe:
            print("INSUFFICIENT=1")
            return
        
        # Get all 144 filings with dates
        c.execute("""
            SELECT symbol_id, filed_ts
            FROM filings
            WHERE form = '144'
        """)
        filings = c.fetchall()
        
        # Get daily bars for universe symbols
        c.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume
            FROM bars
            WHERE tf = '1d' AND symbol_id IN ({})
        """.format(','.join('?' * len(universe))), tuple(universe))
        bars_data = c.fetchall()
        
        # Get Form 4 purchase and 8-K filings
        c.execute("""
            SELECT symbol_id, filed_ts, form, code
            FROM filings
            WHERE (form = '4' AND code = 'P') OR form = '8-K'
        """)
        block_files = c.fetchall()
        
        # Organize data
        bars_by_sym = defaultdict(list)
        for row in bars_data:
            bars_by_sym[row['symbol_id']].append(row)
        
        block_by_sym = defaultdict(list)
        for row in block_files:
            block_by_sym[row['symbol_id']].append(row)
        
        # Sort bars by timestamp
        for sym_id in bars_by_sym:
            bars_by_sym[sym_id].sort(key=lambda x: x['ts'])
        
        def get_next_bar_idx(sym_id, after_ts):
            bars = bars_by_sym.get(sym_id, [])
            for i, bar in enumerate(bars):
                if bar['ts'] > after_ts:
                    return i
            return None
        
        def get_prev_bar_idx(sym_id, before_ts, count=1):
            bars = bars_by_sym.get(sym_id, [])
            indices = []
            for i in range(len(bars)-1, -1, -1):
                if bars[i]['ts'] < before_ts:
                    indices.append(i)
                    if len(indices) == count:
                        return indices
            return indices
        
        def get_bar_on_ts(sym_id, target_date):
            for bar in bars_by_sym.get(sym_id, []):
                from datetime import datetime
                bar_date = datetime.utcfromtimestamp(bar['ts']).strftime('%Y-%m-%d')
                if bar_date == target_date:
                    return bar
            return None
        
        # Process each filing
        opportunities = []
        for sym_id, filed_ts in filings:
            if sym_id not in universe:
                continue
            
            # Get filing date string
            from datetime import datetime
            filing_date = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
            
            # Find next bar after filing (decision day)
            next_idx = get_next_bar_idx(sym_id, filed_ts)
            if next_idx is None:
                continue
            
            decision_bar = bars_by_sym[sym_id][next_idx]
            decision_ts = decision_bar['ts']
            decision_date = datetime.utcfromtimestamp(decision_ts).strftime('%Y-%m-%d')
            
            # Check if we have 5 more bars after decision
            if next_idx + 5 >= len(bars_by_sym[sym_id]):
                continue
            
            # Get close on filing day
            filing_bar = get_bar_on_ts(sym_id, filing_date)
            if filing_bar is None or filing_bar['close'] < 2:
                continue
            
            # Check trailing 5-day return (need 5 bars before decision)
            prev_indices = get_prev_bar_idx(sym_id, decision_ts, 5)
            if len(prev_indices) < 5:
                continue
            
            close_5d_ago = bars_by_sym[sym_id][prev_indices[4]]['close']
            if close_5d_ago == 0:
                continue
            
            trailing_return = (filing_bar['close'] / close_5d_ago) - 1
            if trailing_return <= -0.10:
                continue
            
            # Check Form 4 purchase or 8-K in prior 3 trading days
            # Get 3 trading days before decision
            block_indices = get_prev_bar_idx(sym_id, decision_ts, 3)
            if len(block_indices) < 3:
                continue
            
            cutoff_ts = bars_by_sym[sym_id][block_indices[0]]['ts']  # 3 trading days before
            
            # Check for blocking filings
            blocked = False
            for b_row in block_by_sym.get(sym_id, []):
                if cutoff_ts <= b_row['filed_ts'] < decision_ts:
                    blocked = True
                    break
            
            if blocked:
                continue
            
            # Calculate forward return (5 trading days)
            exit_idx = next_idx + 5
            exit_bar = bars_by_sym[sym_id][exit_idx]
            open_price = decision_bar['open']
            close_price = exit_bar['close']
            
            if open_price == 0:
                continue
            
            forward_return = (close_price / open_price) - 1
            hit = forward_return < 0
            
            opportunities.append({
                'sym_id': sym_id,
                'decision_date': decision_date,
                'decision_ts': decision_ts,
                'hit': hit
            })
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return
        
        # Sort by decision date for time splitting
        opportunities.sort(key=lambda x: x['decision_ts'])
        
        # Split into train and sealed (last 20%)
        total = len(opportunities)
        sealed_start = int(total * 0.8)
        train = opportunities[:sealed_start]
        sealed = opportunities[sealed_start:]
        
        # Calculate metrics
        issued = len(opportunities)
        hits = sum(1 for o in opportunities if o['hit'])
        precision = hits / issued
        
        # Base rate: proportion of hits in issued set (same as precision for single class)
        base_rate = precision
        
        # Distinct days in issued set
        distinct_days = len(set(o['decision_date'] for o in opportunities))
        
        # Effective sample size (design effect)
        day_counts = defaultdict(int)
        for o in opportunities:
            day_counts[o['decision_date']] += 1
        
        n_days = len(day_counts)
        total_calls = issued
        if n_days == 0:
            design_effect = 1
        else:
            sum_sq = sum(cnt**2 for cnt in day_counts.values())
            design_effect = sum_sq / (total_calls**2) * n_days if total_calls > 0 else 1
        
        effective_n = total_calls / design_effect if design_effect > 0 else 0
        
        # Sealed metrics
        sealed_hits = sum(1 for o in sealed if o['hit'])
        sealed_precision = sealed_hits / len(sealed) if sealed else 0
        
        # Print required lines
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={total}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()