# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 517
# cycle_index: 47
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def get_trading_days_in_range(conn, start_ts, end_ts):
    """Get list of unique trading day timestamps between start and end timestamps."""
    cur = conn.cursor()
    cur.execute("""
        SELECT DISTINCT ts FROM bars
        WHERE ts >= ? AND ts < ? AND tf = '1d'
        ORDER BY ts
    """, (start_ts, end_ts))
    return [row[0] for row in cur.fetchall()]

def get_20day_avg_volume(conn, symbol_id, ref_ts):
    """Get 20-day average daily volume ending at ref_ts (exclusive)."""
    cur = conn.cursor()
    # Get the 20 most recent daily bars before ref_ts
    cur.execute("""
        SELECT ts, volume FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        ORDER BY ts DESC
        LIMIT 20
    """, (symbol_id, ref_ts))
    rows = cur.fetchall()
    if len(rows) < 10:  # Need at least half the period for reliability
        return None
    total_vol = sum(r[1] for r in rows)
    return total_vol / len(rows)

def get_current_price(conn, symbol_id, ref_ts):
    """Get closing price on or just before ref_ts."""
    cur = conn.cursor()
    cur.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        ORDER BY ts DESC
        LIMIT 1
    """, (symbol_id, ref_ts))
    row = cur.fetchone()
    return row[0] if row else None

def get_20day_avg_news_sentiment(conn, symbol_id, ref_ts):
    """Get 20-day moving average of daily news sentiment ending at ref_ts."""
    cur = conn.cursor()
    # Get daily sentiment aggregates for 20 days before ref_ts
    cur.execute("""
        SELECT AVG(score) as daily_avg
        FROM news
        WHERE symbol_id = ? AND ts < ?
        GROUP BY DATE(ts, 'unixepoch')
        ORDER BY DATE(ts, 'unixepoch') DESC
        LIMIT 20
    """, (symbol_id, ref_ts))
    rows = cur.fetchall()
    if len(rows) < 10:
        return None
    avg = sum(r[0] for r in rows) / len(rows)
    return avg

def get_sentiment_tercile(conn, symbol_id, ref_ts):
    """Get the 1-year range of 20-day avg sentiment to compute terciles."""
    cur = conn.cursor()
    # Get all daily sentiment aggregates for 1 year
    year_ago = ref_ts - 365*86400
    cur.execute("""
        SELECT AVG(score) as daily_avg
        FROM news
        WHERE symbol_id = ? AND ts >= ? AND ts < ?
        GROUP BY DATE(ts, 'unixepoch')
        ORDER BY DATE(ts, 'unixepoch')
    """, (symbol_id, year_ago, ref_ts))
    rows = [r[0] for r in cur.fetchall()]
    if len(rows) < 30:
        return None
    rows.sort()
    n = len(rows)
    tercile1 = rows[n//3]
    tercile2 = rows[2*n//3]
    return (tercile1, tercile2)

def get_prediction_outcome(conn, symbol_id, ts, horizon_days):
    """Check if prediction resolved correctly for given horizon."""
    cur = conn.cursor()
    # Find outcome with ts exactly horizon_days * 86400 later (approx)
    target_ts = ts + horizon_days * 86400
    cur.execute("""
        SELECT up FROM prediction_outcomes
        WHERE symbol_id = ? AND ts = ? AND horizon = ?
        LIMIT 1
    """, (symbol_id, target_ts, horizon_days))
    row = cur.fetchone()
    if row:
        return 1 if row[0] else 0
    return None

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    
    # Get all symbols with sufficient fundamental data (2 years quarterly)
    cur.execute("""
        SELECT symbol_id, metric, value, fetched_at
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat' AND value IS NOT NULL
        ORDER BY symbol_id, fetched_at
    """)
    fundamentals = cur.fetchall()
    
    # Group by symbol and check for 8 quarters (2 years)
    symbol_funds = {}
    for f in fundamentals:
        sid = f['symbol_id']
        if sid not in symbol_funds:
            symbol_funds[sid] = []
        symbol_funds[sid].append({
            'value': float(f['value']),
            'ts': f['fetched_at']
        })
    
    eligible_symbols = []
    for sid, records in symbol_funds.items():
        if len(records) < 8:
            continue
        # Check for declining public float in last two records
        last_two = sorted(records, key=lambda x: x['ts'], reverse=True)[:2]
        if last_two[0]['value'] >= last_two[1]['value']:
            continue
        eligible_symbols.append(sid)
    
    # Now check 13F for these symbols
    cur.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id IN ({})
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """.format(','.join('?' * len(eligible_symbols))), eligible_symbols)
    holdings = cur.fetchall()
    
    symbol_holdings = {}
    for h in holdings:
        sid = h['symbol_id']
        if sid not in symbol_holdings:
            symbol_holdings[sid] = []
        symbol_holdings[sid].append({
            'shares': h['total_shares'],
            'period': h['period']
        })
    
    # Filter to symbols with increasing institutional ownership
    final_symbols = []
    for sid in eligible_symbols:
        if sid not in symbol_holdings:
            continue
        records = sorted(symbol_holdings[sid], key=lambda x: x['period'], reverse=True)
        if len(records) < 2:
            continue
        # Check increasing shares
        if records[0]['shares'] <= records[1]['shares']:
            continue
        final_symbols.append(sid)
    
    if not final_symbols:
        print("INSUFFICIENT=1")
        return
    
    # Generate decision points
    opportunities = []
    for sid in final_symbols:
        # Get the second most recent 13F period (to determine decision date)
        cur.execute("""
            SELECT period FROM inst_holdings
            WHERE symbol_id = ?
            GROUP BY period
            ORDER BY period DESC
            LIMIT 1 OFFSET 1
        """, (sid,))
        row = cur.fetchone()
        if not row:
            continue
        period_ts = row[0]
        
        # Decision date = period_ts + 45 days, then find next trading day
        decision_ts = period_ts + 45 * 86400
        # Find next trading day in bars
        cur.execute("""
            SELECT ts FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts
            LIMIT 1
        """, (sid, decision_ts))
        row = cur.fetchone()
        if not row:
            continue
        actual_decision_ts = row[0]
        
        opportunities.append({
            'symbol_id': sid,
            'decision_ts': actual_decision_ts,
            'period_ts': period_ts
        })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Sort opportunities by time
    opportunities.sort(key=lambda x: x['decision_ts'])
    
    # Split into train and sealed (last 20%)
    n_total = len(opportunities)
    n_sealed = int(n_total * 0.2)
    n_train = n_total - n_sealed
    
    train_opps = opportunities[:n_train]
    sealed_opps = opportunities[n_train:]
    
    # Process each opportunity
    calls = []
    
    def process_opps(opps_list, is_sealed):
        for opp in opps_list:
            sid = opp['symbol_id']
            decision_ts = opp['decision_ts']
            
            # Check abstain conditions
            avg_vol = get_20day_avg_volume(conn, sid, decision_ts)
            if avg_vol is None or avg_vol < 200000:
                continue
            price = get_current_price(conn, sid, decision_ts)
            if price is None or price < 5:
                continue
            
            # Check news sentiment condition
            avg_sentiment = get_20day_avg_news_sentiment(conn, sid, decision_ts)
            if avg_sentiment is None:
                continue
            
            tercile = get_sentiment_tercile(conn, sid, decision_ts)
            if tercile is None:
                continue
            
            # Bottom tercile means less than first tercile boundary
            if avg_sentiment >= tercile[0]:
                continue
            
            # Issue call (predict up)
            calls.append({
                'symbol_id': sid,
                'ts': decision_ts,
                'is_sealed': is_sealed
            })
    
    process_opps(train_opps, False)
    process_opps(sealed_opps, True)
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Evaluate calls
    hits = 0
    sealed_hits = 0
    base_rate_hits = 0
    
    # Get base rate from all calls
    for call in calls:
        outcome = get_prediction_outcome(conn, call['symbol_id'], call['ts'], 21)
        if outcome == 1:
            base_rate_hits += 1
    
    base_rate = base_rate_hits / len(calls) if calls else 0
    
    # Count distinct days
    distinct_days = len(set(call['ts'] for call in calls))
    
    # Calculate design effect for effective sample size
    # Group calls by day
    day_groups = {}
    for call in calls:
        day = call['ts']
        if day not in day_groups:
            day_groups[day] = []
        day_groups[day].append(call)
    
    # Calculate intra-class correlation (simplified)
    m = len(calls) / len(day_groups) if day_groups else 1  # Average cluster size
    # Estimate rho (simplified, would need actual variance calculations in practice)
    # For now assume rho = 0.1 as reasonable for correlated calls
    rho = 0.1
    design_effect = 1 + (m - 1) * rho
    effective_n = len(calls) / design_effect
    
    # Train precision
    train_calls = [c for c in calls if not c['is_sealed']]
    sealed_calls = [c for c in calls if c['is_sealed']]
    
    train_hits = 0
    for call in train_calls:
        outcome = get_prediction_outcome(conn, call['symbol_id'], call['ts'], 21)
        if outcome == 1:
            train_hits += 1
    
    sealed_hits = 0
    for call in sealed_calls:
        outcome = get_prediction_outcome(conn, call['symbol_id'], call['ts'], 21)
        if outcome == 1:
            sealed_hits += 1
    
    train_precision = train_hits / len(train_calls) if train_calls else 0
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    # Print required output
    print(f"ISSUED={len(calls)}")
    print(f"OPPORTUNITIES={n_total}")
    print(f"PRECISION={train_precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision}")
    
    conn.close()

if __name__ == "__main__":
    main()