import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_horizons(conn):
    cur = conn.execute("SELECT DISTINCT horizon FROM prediction_outcomes")
    return [row[0] for row in cur.fetchall()]

def get_symbols_with_data(conn):
    """Get symbols that have insider trades, fundamentals, bars, and prediction_outcomes."""
    query = """
    SELECT DISTINCT s.id, s.symbol
    FROM symbols s
    JOIN insider_trades it ON it.symbol_id = s.id
    JOIN fundamentals f ON f.symbol_id = s.id
    JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
    JOIN prediction_outcomes po ON po.symbol_id = s.id
    WHERE s.active = 1
    """
    cur = conn.execute(query)
    return cur.fetchall()

def get_fundamentals(conn, symbol_id):
    """Get SharesOutstanding and Revenues quarterly data with fetched_at."""
    query = """
    SELECT metric, value, as_of, fetched_at
    FROM fundamentals
    WHERE symbol_id = ? AND metric IN ('SharesOutstanding', 'Revenues')
    ORDER BY metric, as_of
    """
    cur = conn.execute(query, (symbol_id,))
    rows = cur.fetchall()
    so = []
    rev = []
    for metric, value, as_of, fetched_at in rows:
        if metric == 'SharesOutstanding':
            so.append((as_of, float(value), fetched_at))
        else:
            rev.append((as_of, float(value), fetched_at))
    return so, rev

def get_insider_purchases(conn, symbol_id):
    """Get open-market purchases (code='P') with filed_ts."""
    query = """
    SELECT insider, filed_ts, shares, price
    FROM insider_trades
    WHERE symbol_id = ? AND code = 'P'
    ORDER BY filed_ts
    """
    cur = conn.execute(query, (symbol_id,))
    return cur.fetchall()

def get_daily_bars(conn, symbol_id):
    """Get daily bars (tf='1d')."""
    query = """
    SELECT ts, open, high, low, close, volume
    FROM bars
    WHERE symbol_id = ? AND tf = '1d'
    ORDER BY ts
    """
    cur = conn.execute(query, (symbol_id,))
    return cur.fetchall()

def get_news_sentiment(conn, symbol_id):
    """Get daily news sentiment from sentiment_features (day is YYYY-MM-DD)."""
    query = """
    SELECT day, mean_score
    FROM sentiment_features
    WHERE symbol_id = ?
    ORDER BY day
    """
    cur = conn.execute(query, (symbol_id,))
    return cur.fetchall()

def get_prediction_outcomes(conn, symbol_id, horizon):
    """Get labels for given horizon."""
    query = """
    SELECT ts, up
    FROM prediction_outcomes
    WHERE symbol_id = ? AND horizon = ?
    ORDER BY ts
    """
    cur = conn.execute(query, (symbol_id, horizon))
    return cur.fetchall()

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = connect()
    
    horizons = get_horizons(conn)
    print(f"Available horizons: {horizons}", file=sys.stderr)
    
    # Use 21d horizon if available, else first available
    target_horizon = '21d' if '21d' in horizons else horizons[0]
    print(f"Using horizon: {target_horizon}", file=sys.stderr)
    
    symbols = get_symbols_with_data(conn)
    print(f"Symbols with all data: {len(symbols)}", file=sys.stderr)
    
    all_decisions = []  # (decision_ts, symbol_id, issued, label)
    
    for symbol_id, symbol in symbols:
        # Get all data for this symbol
        so_data, rev_data = get_fundamentals(conn, symbol_id)
        insider_purchases = get_insider_purchases(conn, symbol_id)
        bars = get_daily_bars(conn, symbol_id)
        news_sentiment = get_news_sentiment(conn, symbol_id)
        labels = get_prediction_outcomes(conn, symbol_id, target_horizon)
        
        if not bars or not labels or not insider_purchases:
            continue
        
        # Build lookup maps
        bars_by_ts = {ts: (o, h, l, c, v) for ts, o, h, l, c, v in bars}
        bar_dates = sorted(bars_by_ts.keys())
        
        labels_by_ts = {ts: up for ts, up in labels}
        
        # News sentiment by date string
        news_by_day = {day: score for day, score in news_sentiment}
        
        # Fundamentals: organize by fetched_at (when knowable)
        # For each quarter, we have as_of (period end) and fetched_at (when learned)
        # We need to know, at each decision date, what is the latest quarterly data available
        so_by_fetched = defaultdict(list)
        for as_of, val, fetched in so_data:
            so_by_fetched[fetched].append((as_of, val))
        rev_by_fetched = defaultdict(list)
        for as_of, val, fetched in rev_data:
            rev_by_fetched[fetched].append((as_of, val))
        
        # Insider purchases by filed_ts
        insider_by_filed = defaultdict(list)
        for insider, filed_ts, shares, price in insider_purchases:
            insider_by_filed[filed_ts].append(insider)
        
        # For each bar date (decision point), check conditions
        # We need at least 252 days of history for 252-day avg volume
        for i, decision_ts in enumerate(bar_dates):
            if i < 252:
                continue
            
            decision_date = unix_to_date(decision_ts)
            
            # Get label for this decision_ts and horizon
            label = labels_by_ts.get(decision_ts)
            if label is None:
                continue
            
            # Condition 1: At least 2 distinct insiders filed open-market purchases 
            # within 5 trading days ending on or before decision_ts
            # Look back 5 trading days (approx 7 calendar days)
            insider_window_start = decision_ts - 7 * 86400
            insiders_in_window = set()
            for filed_ts, insiders in insider_by_filed.items():
                if insider_window_start <= filed_ts <= decision_ts:
                    insiders_in_window.update(insiders)
            
            if len(insiders_in_window) < 2:
                continue
            
            # Condition 2: SharesOutstanding decreased QoQ for at least 2 consecutive quarters
            # as of decision_ts (using latest fetched_at <= decision_ts)
            latest_so = []
            for fetched, entries in so_by_fetched.items():
                if fetched <= decision_ts:
                    for as_of, val in entries:
                        latest_so.append((fetched, as_of, val))
            if not latest_so:
                continue
            latest_so.sort(key=lambda x: (x[1], x[0]))  # sort by as_of, then fetched
            # Get latest value for each quarter (as_of)
            so_by_quarter = {}
            for fetched, as_of, val in latest_so:
                if as_of not in so_by_quarter or fetched > so_by_quarter[as_of][0]:
                    so_by_quarter[as_of] = (fetched, val)
            quarters = sorted(so_by_quarter.keys())
            if len(quarters) < 3:
                continue
            # Check last 3 quarters (need 2 QoQ decreases)
            q_vals = [so_by_quarter[q][1] for q in quarters[-3:]]
            if not (q_vals[2] < q_vals[1] < q_vals[0]):
                continue
            
            # Condition 3: Most recent quarterly revenue exceeds prior quarter
            latest_rev = []
            for fetched, entries in rev_by_fetched.items():
                if fetched <= decision_ts:
                    for as_of, val in entries:
                        latest_rev.append((fetched, as_of, val))
            if not latest_rev:
                continue
            latest_rev.sort(key=lambda x: (x[1], x[0]))
            rev_by_quarter = {}
            for fetched, as_of, val in latest_rev:
                if as_of not in rev_by_quarter or fetched > rev_by_quarter[as_of][0]:
                    rev_by_quarter[as_of] = (fetched, val)
            rev_quarters = sorted(rev_by_quarter.keys())
            if len(rev_quarters) < 2:
                continue
            if rev_by_quarter[rev_quarters[-1]][1] <= rev_by_quarter[rev_quarters[-2]][1]:
                continue
            
            # Condition 4: 21-day price return is negative
            if i < 21:
                continue
            price_now = bars_by_ts[decision_ts][3]  # close
            price_21d_ago = bars_by_ts[bar_dates[i-21]][3]
            if price_now >= price_21d_ago:
                continue
            
            # Condition 5: 21-day average volume below 252-day average volume
            vol_21d = sum(bars_by_ts[bar_dates[j]][4] for j in range(i-20, i+1)) / 21
            vol_252d = sum(bars_by_ts[bar_dates[j]][4] for j in range(i-251, i+1)) / 252
            if vol_21d >= vol_252d:
                continue
            
            # Abstain: Any disclosure day in window has news sentiment below 1-year 5th percentile
            # Compute 1-year (252 trading days) news sentiment percentile
            news_scores = []
            for j in range(max(0, i-251), i+1):
                day_str = unix_to_date(bar_dates[j]).isoformat()
                if day_str in news_by_day:
                    news_scores.append(news_by_day[day_str])
            if len(news_scores) >= 20:
                p5 = sorted(news_scores)[max(0, int(len(news_scores) * 0.05) - 1)]
                abstain = False
                for filed_ts in insider_by_filed:
                    if insider_window_start <= filed_ts <= decision_ts:
                        filed_day = unix_to_date(filed_ts).isoformat()
                        if filed_day in news_by_day and news_by_day[filed_day] < p5:
                            abstain = True
                            break
                if abstain:
                    continue
            
            # All conditions met - issue call
            all_decisions.append((decision_ts, symbol_id, 1, label))
    
    if not all_decisions:
        print("INSUFFICIENT=1")
        return
    
    # Sort by decision_ts
    all_decisions.sort(key=lambda x: x[0])
    
    # Split: most recent 20% as sealed era
    n = len(all_decisions)
    split_idx = int(n * 0.8)
    main_decisions = all_decisions[:split_idx]
    sealed_decisions = all_decisions[split_idx:]
    
    def compute_metrics(decisions):
        if not decisions:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = sum(1 for d in decisions if d[2] == 1)
        opportunities = len(decisions)
        hits = sum(1 for d in decisions if d[2] == 1 and d[3] == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = sum(1 for d in decisions if d[2] == 1 and d[3] == 1) / issued if issued > 0 else 0.0
        distinct_days = len(set(unix_to_date(d[0]) for d in decisions if d[2] == 1))
        # Design effect: cluster by day, compute variance inflation
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use rho = 0.5 as conservative estimate for financial returns
        day_counts = defaultdict(int)
        for d in decisions:
            if d[2] == 1:
                day_counts[unix_to_date(d[0])] += 1
        if day_counts:
            avg_cluster = sum(day_counts.values()) / len(day_counts)
            design_effect = 1 + (avg_cluster - 1) * 0.5
            effective_n = issued / design_effect
        else:
            effective_n = 0.0
        return issued, opportunities, precision, base_rate, distinct_days, effective_n
    
    issued, opportunities, precision, base_rate, distinct_days, effective_n = compute_metrics(main_decisions)
    sealed_issued, _, sealed_precision, _, _, _ = compute_metrics(sealed_decisions)
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()