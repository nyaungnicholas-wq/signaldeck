# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 847
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def main():
    con = connect()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Check fundamentals coverage for SharesOutstanding and Revenues
    cur.execute("""
        SELECT symbol_id, metric, COUNT(*) as cnt, MIN(as_of) as min_asof, MAX(as_of) as max_asof
        FROM fundamentals
        WHERE metric IN ('SharesOutstanding', 'Revenues')
        GROUP BY symbol_id, metric
        HAVING cnt >= 12
    """)
    so_symbols = set()
    rev_symbols = set()
    for row in cur.fetchall():
        if row['metric'] == 'SharesOutstanding':
            so_symbols.add(row['symbol_id'])
        else:
            rev_symbols.add(row['symbol_id'])
    
    symbols_with_both = so_symbols & rev_symbols
    print(f"Symbols with 12+ quarters SO: {len(so_symbols)}", file=sys.stderr)
    print(f"Symbols with 12+ quarters Rev: {len(rev_symbols)}", file=sys.stderr)
    print(f"Symbols with both: {len(symbols_with_both)}", file=sys.stderr)
    
    if len(symbols_with_both) == 0:
        print("INSUFFICIENT=1")
        return 0

    # 2. Get SharesOutstanding history for these symbols to check zero-dilution
    # Need quarterly growth <=2% for trailing 12 quarters
    placeholders = ','.join('?' * len(symbols_with_both))
    cur.execute(f"""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, as_of
    """, list(symbols_with_both))
    
    so_data = defaultdict(list)
    for row in cur.fetchall():
        so_data[row['symbol_id']].append((row['as_of'], row['value'], row['fetched_at']))
    
    zero_dilution_symbols = set()
    for sym, rows in so_data.items():
        if len(rows) < 12:
            continue
        # Sort by as_of (period)
        rows.sort(key=lambda x: x[0])
        # Check trailing 12 quarters growth
        ok = True
        for i in range(1, min(12, len(rows))):
            prev_val = rows[i-1][1]
            curr_val = rows[i][1]
            if prev_val > 0:
                growth = (curr_val - prev_val) / prev_val
                if growth > 0.02:
                    ok = False
                    break
        if ok:
            zero_dilution_symbols.add(sym)
    
    print(f"Zero-dilution symbols: {len(zero_dilution_symbols)}", file=sys.stderr)
    if not zero_dilution_symbols:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get officer (CEO/CFO) open-market purchases (code='P') for these symbols
    placeholders = ','.join('?' * len(zero_dilution_symbols))
    cur.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code = 'P'
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY symbol_id, tx_ts
    """, list(zero_dilution_symbols))
    
    trades = cur.fetchall()
    print(f"Officer open-market purchases: {len(trades)}", file=sys.stderr)
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Group trades by symbol and officer
    trades_by_sym_officer = defaultdict(list)
    for t in trades:
        key = (t['symbol_id'], t['insider'])
        trades_by_sym_officer[key].append(t)

    # 4. Get Revenue history for zero-dilution symbols
    cur.execute(f"""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric = 'Revenues' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, as_of
    """, list(zero_dilution_symbols))
    
    rev_data = defaultdict(list)
    for row in cur.fetchall():
        rev_data[row['symbol_id']].append((row['as_of'], row['value'], row['fetched_at']))
    
    for sym in rev_data:
        rev_data[sym].sort(key=lambda x: x[0])

    # 5. Get daily bars for 252-day return calculation
    # We'll fetch on demand per symbol

    # 6. Process each trade as potential entry
    entries = []  # (decision_date, symbol_id, tx_ts, filed_ts, forward_return)
    
    for (sym, officer), officer_trades in trades_by_sym_officer.items():
        if sym not in rev_data or len(rev_data[sym]) < 3:
            continue
        
        # Compute officer's 75th percentile historical trade size (value)
        hist_values = [t['value'] for t in officer_trades]
        hist_values.sort()
        p75_idx = int(len(hist_values) * 0.75)
        p75_value = hist_values[p75_idx] if p75_idx < len(hist_values) else hist_values[-1]
        
        # For each trade, check conditions
        for i, trade in enumerate(officer_trades):
            tx_ts = trade['tx_ts']
            filed_ts = trade['filed_ts']
            trade_value = trade['value']
            
            # Condition 4: trade size exceeds officer's 75th percentile historical trade size
            # Use only trades BEFORE this one for historical percentile
            prior_values = [t['value'] for t in officer_trades[:i]]
            if len(prior_values) < 5:  # Need some history
                continue
            prior_values.sort()
            p75_idx_prior = int(len(prior_values) * 0.75)
            p75_prior = prior_values[p75_idx_prior] if p75_idx_prior < len(prior_values) else prior_values[-1]
            if trade_value <= p75_prior:
                continue
            
            # Condition 3: Form 4 disclosure date within 2 sessions of T
            # Sessions = trading days. Approximate: 2 sessions ~ 2-3 calendar days
            tx_date = epoch_to_date(tx_ts)
            filed_date = epoch_to_date(filed_ts)
            if (filed_date - tx_date).days > 3:  # Allow 3 calendar days for 2 sessions
                continue
            
            # Condition 2: 252-session price return as of T is negative
            # Need bars up to tx_ts (trade date)
            cur.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                ORDER BY ts DESC LIMIT 253
            """, (sym, tx_ts))
            bars = cur.fetchall()
            if len(bars) < 253:
                continue
            close_now = bars[0]['close']
            close_252 = bars[252]['close']
            ret_252 = (close_now - close_252) / close_252
            if ret_252 >= 0:
                continue
            
            # Condition 1: Quarterly revenue growth accelerated for 2 most recent reported quarters prior to T
            # Revenue growth acceleration means: (Rev_q - Rev_q-1)/Rev_q-1 > (Rev_q-1 - Rev_q-2)/Rev_q-2
            # Need at least 3 quarters reported before filed_ts (since we only know at filing)
            # But the condition says "prior to T" (trade date). However as-of discipline: we can only use data known at decision time (filed_ts).
            # Revenue data: fetched_at is when we learned it. Must have fetched_at <= filed_ts.
            rev_quarters = [(as_of, val, fetched) for as_of, val, fetched in rev_data[sym] if fetched <= filed_ts]
            if len(rev_quarters) < 3:
                continue
            rev_quarters.sort(key=lambda x: x[0], reverse=True)  # Most recent first
            # Take 3 most recent
            q0_asof, q0_val, _ = rev_quarters[0]
            q1_asof, q1_val, _ = rev_quarters[1]
            q2_asof, q2_val, _ = rev_quarters[2]
            
            if q1_val <= 0 or q2_val <= 0:
                continue
            growth_q1 = (q0_val - q1_val) / q1_val
            growth_q2 = (q1_val - q2_val) / q2_val
            if growth_q1 <= growth_q2:  # Not accelerating
                continue
            
            # All conditions met. This is an entry.
            # Decision timestamp is filed_ts (when we know about the trade)
            # But label horizon is 63 calendar days from trade date T (tx_ts) or from decision?
            # Hypothesis: "HORIZON: 63 calendar days" - typically from entry decision.
            # Entry decision is at filing date. But the trade happened at T.
            # Let's use 63 calendar days from tx_ts (trade date) as the holding period.
            # Compute forward return from bars: 63 trading days ~ 63 calendar days? 
            # 63 calendar days ~ 45 trading days. But hypothesis says 63 calendar days.
            # Use bars tf='1d', find close at tx_ts and close at tx_ts + 63 days.
            start_ts = tx_ts
            end_ts = tx_ts + 63 * 86400  # 63 calendar days in seconds
            
            cur.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
                ORDER BY ts
            """, (sym, start_ts - 86400, end_ts + 86400))  # Small buffer
            fwd_bars = cur.fetchall()
            if len(fwd_bars) < 2:
                continue
            # Find closest to start_ts and end_ts
            start_close = None
            end_close = None
            for b in fwd_bars:
                if b['ts'] >= start_ts and start_close is None:
                    start_close = b['close']
                if b['ts'] >= end_ts:
                    end_close = b['close']
                    break
            if start_close is None or end_close is None or start_close == 0:
                continue
            fwd_ret = (end_close - start_close) / start_close
            up = 1 if fwd_ret > 0 else 0
            
            decision_date = epoch_to_date(filed_ts)  # Decision at filing
            entries.append((decision_date, sym, tx_ts, filed_ts, up, fwd_ret))

    print(f"Total entries (issued calls): {len(entries)}", file=sys.stderr)
    if len(entries) == 0:
        print("INSUFFICIENT=1")
        return 0

    # 7. Hold out most recent 20% as sealed era
    entries.sort(key=lambda x: x[0])  # Sort by decision_date
    n = len(entries)
    split_idx = int(n * 0.8)
    train_entries = entries[:split_idx]
    sealed_entries = entries[split_idx:]

    def compute_metrics(entries_list):
        if not entries_list:
            return 0, 0, 0, 0, 0
        issued = len(entries_list)
        hits = sum(1 for e in entries_list if e[4] == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # Base rate of predicted class (up) within issued subset
        distinct_days = len(set(e[0] for e in entries_list))
        # Design effect: cluster by symbol-day? Or time clustering.
        # Simple approach: group by decision_date, count per day, design effect = 1 + (avg_cluster_size - 1) * ICC
        # But we don't have ICC. Use Kish's effective sample size: n_eff = (sum w)^2 / sum w^2 where w=1
        # For clustered data, approximate: group by day, effective_n = (sum n_d)^2 / sum n_d^2
        day_counts = defaultdict(int)
        for e in entries_list:
            day_counts[e[0]] += 1
        sum_n = sum(day_counts.values())
        sum_n2 = sum(c*c for c in day_counts.values())
        effective_n = (sum_n * sum_n) / sum_n2 if sum_n2 > 0 else 0
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days, train_effective_n = compute_metrics(train_entries)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_entries)

    # Opportunities: count of decision points considered
    # This is tricky. The hypothesis says "ABSTAIN: No officer purchase satisfying all four entry conditions on a given symbol-day"
    # So opportunities are symbol-days where we checked conditions.
    # We checked each officer trade as a potential entry. But multiple trades per symbol-day?
    # Let's count unique (symbol, decision_date) pairs we evaluated.
    # Actually we evaluated each trade. But the abstention is per symbol-day.
    # Let's approximate: opportunities = number of unique (symbol, filed_date) where an officer trade occurred and we checked conditions.
    evaluated_opportunities = set()
    for (sym, officer), officer_trades in trades_by_sym_officer.items():
        if sym not in zero_dilution_symbols:
            continue
        for trade in officer_trades:
            filed_date = epoch_to_date(trade['filed_ts'])
            evaluated_opportunities.add((sym, filed_date))
    
    opportunities = len(evaluated_opportunities)

    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_precision:.6f}")
    print(f"BASE_RATE={train_base_rate:.6f}")
    print(f"DISTINCT_DAYS={train_distinct_days}")
    print(f"EFFECTIVE_N={train_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())