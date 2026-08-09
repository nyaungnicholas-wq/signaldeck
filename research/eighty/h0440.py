# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 439
# cycle_index: 30
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

def get_universe_symbols(conn):
    """Get symbols meeting universe criteria."""
    cur = conn.cursor()
    
    # Symbols with at least 2 years of daily bars (1d)
    # 2 years = ~730 days, but we need 60-day lookbacks so need more
    cur.execute("""
        SELECT symbol_id, COUNT(*) as bar_count, MIN(ts) as min_ts, MAX(ts) as max_ts
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING bar_count >= 730
    """)
    symbols_with_bars = {row[0]: (row[2], row[3]) for row in cur.fetchall()}
    
    # Symbols with insider history
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    symbols_with_insider = set(row[0] for row in cur.fetchall())
    
    # Symbols with quarterly fundamentals (SharesOutstanding)
    cur.execute("""
        SELECT DISTINCT symbol_id FROM fundamentals 
        WHERE metric = 'SharesOutstanding'
    """)
    symbols_with_fundamentals = set(row[0] for row in cur.fetchall())
    
    # Symbols with daily news sentiment
    cur.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
    symbols_with_sentiment = set(row[0] for row in cur.fetchall())
    
    # Intersection
    candidate_symbols = (set(symbols_with_bars.keys()) & symbols_with_insider & 
                         symbols_with_fundamentals & symbols_with_sentiment)
    
    if not candidate_symbols:
        return []
    
    # Top 80% by 20-day average daily dollar volume (using most recent data)
    # Compute 20-day avg dollar volume for each symbol
    placeholders = ','.join('?' * len(candidate_symbols))
    cur.execute(f"""
        SELECT symbol_id, AVG(close * volume) as avg_dollar_vol
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        GROUP BY symbol_id
    """, list(candidate_symbols))
    
    dollar_vols = {row[0]: row[1] for row in cur.fetchall() if row[1] is not None}
    
    if not dollar_vols:
        return []
    
    # Top 80%
    sorted_symbols = sorted(dollar_vols.items(), key=lambda x: x[1], reverse=True)
    cutoff = int(len(sorted_symbols) * 0.8)
    universe_symbols = [s[0] for s in sorted_symbols[:cutoff]]
    
    return universe_symbols

def get_shares_outstanding_history(conn, symbol_id):
    """Get quarterly SharesOutstanding history for a symbol, ordered by as_of."""
    cur = conn.cursor()
    cur.execute("""
        SELECT as_of, value, fetched_at
        FROM fundamentals
        WHERE symbol_id = ? AND metric = 'SharesOutstanding'
        ORDER BY as_of
    """, (symbol_id,))
    return [(row[0], float(row[1]), row[2]) for row in cur.fetchall()]

def get_daily_bars(conn, symbol_id, start_ts=None, end_ts=None):
    """Get daily bars for a symbol."""
    cur = conn.cursor()
    query = "SELECT ts, open, high, low, close, volume FROM bars WHERE tf = '1d' AND symbol_id = ?"
    params = [symbol_id]
    if start_ts is not None:
        query += " AND ts >= ?"
        params.append(start_ts)
    if end_ts is not None:
        query += " AND ts <= ?"
        params.append(end_ts)
    query += " ORDER BY ts"
    cur.execute(query, params)
    return [(row[0], row[1], row[2], row[3], row[4], row[5]) for row in cur.fetchall()]

def get_sentiment_features(conn, symbol_id, start_day=None, end_day=None):
    """Get daily sentiment features for a symbol."""
    cur = conn.cursor()
    query = "SELECT day, mean_score FROM sentiment_features WHERE symbol_id = ?"
    params = [symbol_id]
    if start_day is not None:
        query += " AND day >= ?"
        params.append(start_day)
    if end_day is not None:
        query += " AND day <= ?"
        params.append(end_day)
    query += " ORDER BY day"
    cur.execute(query, params)
    return [(row[0], float(row[1])) for row in cur.fetchall()]

def get_insider_purchases(conn, symbol_id, start_ts=None, end_ts=None):
    """Get open-market insider purchases (code=P) for a symbol, by filed_ts."""
    cur = conn.cursor()
    query = """
        SELECT filed_ts, tx_ts, shares, price, value
        FROM insider_trades
        WHERE symbol_id = ? AND code = 'P'
    """
    params = [symbol_id]
    if start_ts is not None:
        query += " AND filed_ts >= ?"
        params.append(start_ts)
    if end_ts is not None:
        query += " AND filed_ts <= ?"
        params.append(end_ts)
    query += " ORDER BY filed_ts"
    cur.execute(query, params)
    return [(row[0], row[1], row[2], row[3], row[4]) for row in cur.fetchall()]

def get_prediction_outcome(conn, symbol_id, horizon, ts):
    """Get label from prediction_outcomes for given symbol, horizon, ts."""
    cur = conn.cursor()
    cur.execute("""
        SELECT up, fwd_return FROM prediction_outcomes
        WHERE symbol_id = ? AND horizon = ? AND ts = ?
    """, (symbol_id, horizon, ts))
    row = cur.fetchone()
    if row:
        return row[0], row[1]
    return None, None

def ts_to_date(ts):
    """Convert unix timestamp to YYYY-MM-DD string."""
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_ts(date_str):
    """Convert YYYY-MM-DD to unix timestamp (start of day UTC)."""
    return int(datetime.strptime(date_str, '%Y-%m-%d').timestamp())

def find_decision_points(conn, universe_symbols):
    """Find all valid decision points (symbol, ts) where we have sufficient history."""
    decision_points = []
    
    for symbol_id in universe_symbols:
        # Get daily bars
        bars = get_daily_bars(conn, symbol_id)
        if len(bars) < 252:  # Need at least ~1 year for 60-day lookbacks
            continue
        
        # Get sentiment features
        sentiment = get_sentiment_features(conn, symbol_id)
        if len(sentiment) < 60:
            continue
        
        # Get shares outstanding history
        so_history = get_shares_outstanding_history(conn, symbol_id)
        if len(so_history) < 2:
            continue
        
        # Get insider purchases
        insider_purchases = get_insider_purchases(conn, symbol_id)
        
        # Build lookup maps
        bars_by_ts = {b[0]: b for b in bars}
        sentiment_by_day = {s[0]: s[1] for s in sentiment}
        insider_by_filed = defaultdict(list)
        for ip in insider_purchases:
            insider_by_filed[ip[0]].append(ip)
        
        # For each bar timestamp (potential decision point), check if we have enough history
        # Need 60 days of bars and sentiment before decision
        for i in range(60, len(bars) - 21):  # Leave 21 days for forward return
            decision_ts = bars[i][0]
            decision_day = ts_to_date(decision_ts)
            
            # Check if we have sentiment for this day and past 60 days
            # sentiment features use day strings
            past_days = []
            for j in range(i-60, i+1):
                day = ts_to_date(bars[j][0])
                if day in sentiment_by_day:
                    past_days.append(sentiment_by_day[day])
            
            if len(past_days) < 60:
                continue
            
            # Check shares outstanding: need most recent quarterly as of decision_ts
            # fetched_at must be <= decision_ts (as-of discipline)
            so_as_of_decision = [(as_of, val, fetched) for as_of, val, fetched in so_history 
                                  if fetched <= decision_ts]
            if len(so_as_of_decision) < 2:
                continue
            
            # Most recent and prior quarter
            so_as_of_decision.sort(key=lambda x: x[0], reverse=True)
            most_recent_so = so_as_of_decision[0][1]
            prior_so = so_as_of_decision[1][1]
            
            # Condition 1: SharesOutstanding >=5% lower than prior quarter
            if most_recent_so >= prior_so * 0.95:
                continue
            
            # Condition 2: One-day news sentiment increase >=2 std dev above 60-day mean within past 5 days
            # Compute 60-day mean and std of daily changes
            daily_changes = [past_days[k] - past_days[k-1] for k in range(1, len(past_days))]
            if len(daily_changes) < 2:
                continue
            mean_change = sum(daily_changes) / len(daily_changes)
            std_change = (sum((c - mean_change)**2 for c in daily_changes) / len(daily_changes))**0.5
            
            if std_change == 0:
                continue
            
            threshold = mean_change + 2 * std_change
            
            # Check past 5 days (including today) for spike
            spike_found = False
            for k in range(max(1, len(past_days)-5), len(past_days)):
                change = past_days[k] - past_days[k-1]
                if change >= threshold:
                    spike_found = True
                    break
            
            if not spike_found:
                continue
            
            # Condition 3: Open-market insider purchase disclosed within past 5 days
            # filed_ts within [decision_ts - 5 days, decision_ts]
            five_days_sec = 5 * 86400
            insider_found = False
            for filed_ts in insider_by_filed:
                if decision_ts - five_days_sec <= filed_ts <= decision_ts:
                    insider_found = True
                    break
            
            if not insider_found:
                continue
            
            # ABSTAIN: Stock down >10% in past 5 days
            if i >= 5:
                close_now = bars[i][4]
                close_5d_ago = bars[i-5][4]
                if close_5d_ago > 0 and (close_now - close_5d_ago) / close_5d_ago < -0.10:
                    continue
            
            # ABSTAIN: Daily range in top decile of 60-day range
            # Daily range = (high - low) / close
            ranges_60d = []
            for j in range(i-60, i):
                high, low, close = bars[j][2], bars[j][3], bars[j][4]
                if close > 0:
                    ranges_60d.append((high - low) / close)
            
            if len(ranges_60d) < 10:
                continue
            
            ranges_60d.sort()
            top_decile_threshold = ranges_60d[int(len(ranges_60d) * 0.9)]
            today_range = (bars[i][2] - bars[i][3]) / bars[i][4] if bars[i][4] > 0 else 0
            
            if today_range >= top_decile_threshold:
                continue
            
            # All conditions met - this is a decision point
            decision_points.append((symbol_id, decision_ts, decision_day))
    
    return decision_points

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    
    print("Finding universe symbols...", file=sys.stderr)
    universe_symbols = get_universe_symbols(conn)
    print(f"Universe size: {len(universe_symbols)}", file=sys.stderr)
    
    if not universe_symbols:
        print("INSUFFICIENT=1")
        return
    
    print("Finding decision points...", file=sys.stderr)
    decision_points = find_decision_points(conn, universe_symbols)
    print(f"Decision points: {len(decision_points)}", file=sys.stderr)
    
    if not decision_points:
        print("INSUFFICIENT=1")
        return
    
    # Sort by timestamp
    decision_points.sort(key=lambda x: x[1])
    
    # Hold out most recent 20% as sealed era
    split_idx = int(len(decision_points) * 0.8)
    train_points = decision_points[:split_idx]
    sealed_points = decision_points[split_idx:]
    
    def evaluate_points(points, label):
        issued = 0
        hits = 0
        issued_days = set()
        
        for symbol_id, decision_ts, decision_day in points:
            up, fwd_return = get_prediction_outcome(conn, symbol_id, 21, decision_ts)
            if up is not None:
                issued += 1
                issued_days.add(decision_day)
                if up == 1:
                    hits += 1
        
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of positive class within issued
        
        # Compute design effect for EFFECTIVE_N
        # Simple approach: cluster by day, design effect = 1 + (avg_cluster_size - 1) * ICC
        # Use conservative ICC of 0.1 for financial returns
        day_counts = defaultdict(int)
        for symbol_id, decision_ts, decision_day in points:
            up, _ = get_prediction_outcome(conn, symbol_id, 21, decision_ts)
            if up is not None:
                day_counts[decision_day] += 1
        
        if day_counts:
            avg_cluster_size = sum(day_counts.values()) / len(day_counts)
            icc = 0.1
            design_effect = 1 + (avg_cluster_size - 1) * icc
            effective_n = issued / design_effect if design_effect > 1 else issued
        else:
            effective_n = 0
        
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_HITS={hits}")
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={len(issued_days)}")
        print(f"{label}_EFFECTIVE_N={effective_n:.2f}")
        
        return issued, hits, precision, base_rate, len(issued_days), effective_n
    
    print("Evaluating training era...", file=sys.stderr)
    train_issued, train_hits, train_precision, train_base_rate, train_days, train_eff_n = evaluate_points(train_points, "TRAIN")
    
    print("Evaluating sealed era...", file=sys.stderr)
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_days, sealed_eff_n = evaluate_points(sealed_points, "SEALED")
    
    # Overall
    total_issued = train_issued + sealed_issued
    total_hits = train_hits + sealed_hits
    total_precision = total_hits / total_issued if total_issued > 0 else 0.0
    total_base_rate = total_hits / total_issued if total_issued > 0 else 0.0
    total_days = len(set().union(
        {dp[2] for dp in train_points if get_prediction_outcome(conn, dp[0], 21, dp[1])[0] is not None},
        {dp[2] for dp in sealed_points if get_prediction_outcome(conn, dp[0], 21, dp[1])[0] is not None}
    ))
    
    # Overall effective N
    all_day_counts = defaultdict(int)
    for dp in decision_points:
        up, _ = get_prediction_outcome(conn, dp[0], 21, dp[1])
        if up is not None:
            all_day_counts[dp[2]] += 1
    
    if all_day_counts:
        avg_cluster_size = sum(all_day_counts.values()) / len(all_day_counts)
        icc = 0.1
        design_effect = 1 + (avg_cluster_size - 1) * icc
        total_eff_n = total_issued / design_effect if design_effect > 1 else total_issued
    else:
        total_eff_n = 0
    
    # Print required output
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(decision_points)}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base_rate:.6f}")
    print(f"DISTINCT_DAYS={total_days}")
    print(f"EFFECTIVE_N={total_eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    # Verify invariants
    if total_days > total_issued:
        print(f"ERROR: DISTINCT_DAYS ({total_days}) > ISSUED ({total_issued})", file=sys.stderr)
        sys.exit(1)
    
    if total_eff_n >= total_issued:
        print(f"ERROR: EFFECTIVE_N ({total_eff_n}) >= ISSUED ({total_issued})", file=sys.stderr)
        sys.exit(1)

if __name__ == '__main__':
    main()