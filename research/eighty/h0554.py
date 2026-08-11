import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get all active insider purchases (code='P')
    insider_sql = """
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P'
        AND symbol_id IN (SELECT id FROM symbols WHERE active=1 AND market='stocks')
        ORDER BY filed_ts
    """
    insider_trades = conn.execute(insider_sql).fetchall()
    
    if len(insider_trades) < 10:
        print("INSUFFICIENT=1")
        return
    
    # Get unemployment macro data (UNRATE)
    unemp_sql = """
        SELECT ts, value
        FROM macro_series
        WHERE series='UNRATE'
        ORDER BY ts
    """
    unemp_rows = conn.execute(unemp_sql).fetchall()
    
    # Build unemployment change indicator (current < previous month)
    unemp_months = {}
    for row in unemp_rows:
        dt = datetime.utcfromtimestamp(row['ts']).date()
        ym = (dt.year, dt.month)
        unemp_months[ym] = row['value']
    
    # Sort months and compute changes
    sorted_months = sorted(unemp_months.items())
    unemp_down = {}
    for i in range(1, len(sorted_months)):
        prev_ym, prev_val = sorted_months[i-1]
        curr_ym, curr_val = sorted_months[i]
        # Store for first day of current month
        dt = datetime(curr_ym[0], curr_ym[1], 1).date()
        unemp_down[dt] = curr_val < prev_val
    
    # Process each insider trade
    issued = []
    opportunities = 0
    
    for trade in insider_trades:
        symbol_id = trade['symbol_id']
        filed_ts = trade['filed_ts']
        tx_ts = trade['tx_ts']
        
        # Convert to dates
        filed_dt = datetime.utcfromtimestamp(filed_ts).date()
        tx_dt = datetime.utcfromtimestamp(tx_ts).date()
        
        # Condition: trade within 2 days of disclosure
        if abs((filed_dt - tx_dt).days) > 2:
            continue
        
        opportunities += 1
        
        # Check symbol has sufficient data
        has_bars = conn.execute(
            "SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=?",
            (symbol_id, int(filed_dt.strftime('%s')))
        ).fetchone()[0] > 0
        
        has_sentiment = conn.execute(
            "SELECT COUNT(*) FROM sentiment_features WHERE symbol_id=? AND day<=?",
            (symbol_id, filed_dt.isoformat())
        ).fetchone()[0] > 0
        
        if not (has_bars and has_sentiment):
            continue
        
        # Condition: traded in last 10 days
        recent_sql = """
            SELECT ts FROM bars 
            WHERE symbol_id=? AND tf='1d' AND ts<=?
            ORDER BY ts DESC LIMIT 1
        """
        recent = conn.execute(recent_sql, (symbol_id, int(filed_dt.strftime('%s')))).fetchone()
        if not recent:
            continue
        latest_ts = recent['ts']
        days_since = (filed_dt - datetime.utcfromtimestamp(latest_ts).date()).days
        if days_since > 10:
            continue
        
        # Condition: unemployment down
        # Find most recent month <= filed_dt
        current_ym = (filed_dt.year, filed_dt.month)
        unemp_condition = False
        for dt, is_down in unemp_down.items():
            if dt <= filed_dt:
                unemp_condition = is_down
                # Keep checking for most recent
        if not unemp_condition:
            continue
        
        # Condition: sentiment 5-day MA < 20-day MA
        sent_sql = """
            SELECT day, mean_score FROM sentiment_features
            WHERE symbol_id=? AND day<=?
            ORDER BY day DESC
        """
        sent_rows = conn.execute(sent_sql, (symbol_id, filed_dt.isoformat())).fetchall()
        if len(sent_rows) < 20:
            continue
        
        scores = [row['mean_score'] for row in sent_rows]
        ma5 = sum(scores[:5]) / 5
        ma20 = sum(scores[:20]) / 20
        if not (ma5 < ma20):
            continue
        
        # Get current price (on or before filed_dt)
        price_sql = """
            SELECT close FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts<=?
            ORDER BY ts DESC LIMIT 1
        """
        current_row = conn.execute(price_sql, (symbol_id, int(filed_dt.strftime('%s')))).fetchone()
        if not current_row:
            continue
        current_close = current_row['close']
        if current_close <= 0:
            continue
        
        # Get forward price (21 trading days later)
        # First get the current bar timestamp
        current_bar_sql = """
            SELECT ts FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts<=?
            ORDER BY ts DESC LIMIT 1
        """
        current_bar = conn.execute(current_bar_sql, (symbol_id, int(filed_dt.strftime('%s')))).fetchone()
        if not current_bar:
            continue
        current_ts = current_bar['ts']
        
        # Get 21st bar after current
        forward_sql = """
            SELECT close FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts>?
            ORDER BY ts ASC LIMIT 1 OFFSET 20
        """
        forward_row = conn.execute(forward_sql, (symbol_id, current_ts)).fetchone()
        if not forward_row:
            continue
        forward_close = forward_row['close']
        if forward_close <= 0:
            continue
        
        up = 1 if forward_close > current_close else 0
        
        issued.append({
            'symbol_id': symbol_id,
            'date': filed_dt,
            'up': up
        })
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and sealed (most recent 20% of distinct dates)
    all_dates = sorted(set(call['date'] for call in issued))
    split_idx = int(len(all_dates) * 0.8)
    sealed_start = all_dates[split_idx]
    
    train_calls = [c for c in issued if c['date'] < sealed_start]
    sealed_calls = [c for c in issued if c['date'] >= sealed_start]
    
    if not train_calls or not sealed_calls:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    issued_count = len(issued)
    train_hits = sum(c['up'] for c in train_calls)
    sealed_hits = sum(c['up'] for c in sealed_calls)
    
    precision = train_hits / len(train_calls)
    base_rate = sum(c['up'] for c in issued) / issued_count
    
    distinct_days = len(set(c['date'] for c in issued))
    
    # Design effect calculation (cluster by date)
    day_groups = defaultdict(list)
    for call in issued:
        day_groups[call['date']].append(call['up'])
    
    k = len(day_groups)  # number of clusters
    n = issued_count
    
    if k == 1:
        design_effect = n
    else:
        overall_mean = sum(c['up'] for c in issued) / n
        
        # Between-cluster variance
        ss_between = 0
        for date, outcomes in day_groups.items():
            m_j = len(outcomes)
            p_j = sum(outcomes) / m_j
            ss_between += m_j * (p_j - overall_mean) ** 2
        
        # Within-cluster variance
        ss_within = 0
        for date, outcomes in day_groups.items():
            m_j = len(outcomes)
            p_j = sum(outcomes) / m_j
            for y in outcomes:
                ss_within += (y - p_j) ** 2
        
        ms_between = ss_between / (k - 1)
        ms_within = ss_within / (n - k)
        
        # Average cluster size (excluding self)
        total_sq = sum(len(day_groups[d]) ** 2 for d in day_groups)
        m0 = (n - total_sq / n) / (k - 1)
        
        # ICC
        if ms_between + (m0 - 1) * ms_within == 0:
            icc = 0
        else:
            icc = (ms_between - ms_within) / (ms_between + (m0 - 1) * ms_within)
        
        design_effect = 1 + (m0 - 1) * icc
    
    effective_n = n / design_effect
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    # Print results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()