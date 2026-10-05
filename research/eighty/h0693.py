# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 692
# cycle_index: 19
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

def epoch_to_date(epoch):
    return datetime.utcfromtimestamp(epoch).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def business_days_between(start_ts, end_ts):
    start = epoch_to_date(start_ts)
    end = epoch_to_date(end_ts)
    days = 0
    cur = start
    while cur <= end:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days

def get_spy_symbol_id(conn):
    cur = conn.execute("SELECT id FROM symbols WHERE symbol = 'SPY' AND market = 'stocks'")
    row = cur.fetchone()
    return row[0] if row else None

def get_eligible_symbols(conn, min_date_epoch):
    """Symbols with 252-day history and 63-day avg dollar volume > $1M as of min_date_epoch"""
    cur = conn.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    candidates = cur.fetchall()
    
    eligible = []
    for sym_id, symbol in candidates:
        # Check 252-day history before min_date_epoch
        cur = conn.execute("""
            SELECT COUNT(*) FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        """, (sym_id, min_date_epoch))
        if cur.fetchone()[0] < 252:
            continue
        
        # Check 63-day avg dollar volume > $1M
        cur = conn.execute("""
            SELECT AVG(close * volume) FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ?
            ORDER BY ts DESC LIMIT 63
        """, (sym_id, min_date_epoch))
        avg_dollar_vol = cur.fetchone()[0]
        if avg_dollar_vol is None or avg_dollar_vol <= 1_000_000:
            continue
        
        eligible.append((sym_id, symbol))
    return eligible

def get_spy_returns(conn, spy_id, start_epoch, end_epoch):
    """Get SPY daily returns for a period"""
    cur = conn.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (spy_id, start_epoch, end_epoch))
    rows = cur.fetchall()
    if len(rows) < 2:
        return {}
    returns = {}
    prev_close = rows[0][1]
    for ts, close in rows[1:]:
        ret = (close - prev_close) / prev_close
        returns[ts] = ret
        prev_close = close
    return returns

def compute_63day_return(conn, symbol_id, decision_ts):
    """Compute 63-trading-day return ending at decision_ts (exclusive)"""
    cur = conn.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts < ?
        ORDER BY ts DESC LIMIT 64
    """, (symbol_id, decision_ts))
    rows = cur.fetchall()
    if len(rows) < 64:
        return None
    # rows[0] is most recent (day before decision), rows[63] is 63 days before
    close_now = rows[0][1]
    close_then = rows[63][1]
    return (close_now - close_then) / close_then

def get_news_sentiment_percentile(conn, symbol_id, decision_ts):
    """Get 63-day median sentiment and 252-day 25th percentile"""
    # 63-day median
    cur = conn.execute("""
        SELECT sentiment FROM news
        WHERE symbol_id = ? AND ts < ?
        ORDER BY ts DESC LIMIT 63
    """, (symbol_id, decision_ts))
    sent_63 = [r[0] for r in cur.fetchall() if r[0] is not None]
    if len(sent_63) < 30:  # need reasonable coverage
        return None, None
    sent_63.sort()
    median_63 = sent_63[len(sent_63)//2]
    
    # 252-day 25th percentile
    cur = conn.execute("""
        SELECT sentiment FROM news
        WHERE symbol_id = ? AND ts < ?
        ORDER BY ts DESC LIMIT 252
    """, (symbol_id, decision_ts))
    sent_252 = [r[0] for r in cur.fetchall() if r[0] is not None]
    if len(sent_252) < 100:
        return None, None
    sent_252.sort()
    p25_252 = sent_252[len(sent_252)//4]
    
    return median_63, p25_252

def get_revenue_growth(conn, symbol_id, decision_ts):
    """Check if quarterly revenues increased for >=3 consecutive quarters ending before decision_ts"""
    # Get revenue data with fetched_at < decision_ts (as-of discipline)
    cur = conn.execute("""
        SELECT value, as_of, fetched_at FROM fundamentals
        WHERE symbol_id = ? AND metric = 'Revenues' AND fetched_at < ? AND as_of > 0
        ORDER BY as_of
    """, (symbol_id, decision_ts))
    rows = cur.fetchall()
    if len(rows) < 4:
        return False
    
    # Check last 4+ quarters for 3 consecutive increases
    revenues = [(r[0], r[1]) for r in rows]
    revenues.sort(key=lambda x: x[1])  # sort by as_of
    
    consecutive = 0
    for i in range(1, len(revenues)):
        if revenues[i][0] > revenues[i-1][0]:
            consecutive += 1
            if consecutive >= 3:
                return True
        else:
            consecutive = 0
    return False

def get_insider_purchases(conn, symbol_id, decision_ts):
    """Get open-market purchases (code='P') filed on or before decision_ts with tx_ts <= filed_ts"""
    cur = conn.execute("""
        SELECT insider, tx_ts, filed_ts FROM insider_trades
        WHERE symbol_id = ? AND code = 'P' AND filed_ts <= ? AND tx_ts <= filed_ts
        ORDER BY filed_ts
    """, (symbol_id, decision_ts))
    return cur.fetchall()

def check_entry_conditions(conn, symbol_id, decision_ts, spy_returns, spy_id):
    """Check all entry conditions for a symbol at decision_ts (filed_ts of insider trade)"""
    # Condition (a): 63-day median sentiment < 252-day 25th percentile
    median_63, p25_252 = get_news_sentiment_percentile(conn, symbol_id, decision_ts)
    if median_63 is None or p25_252 is None:
        return False
    if median_63 >= p25_252:
        return False
    
    # Condition (b): Revenue growth >=3 consecutive quarters
    if not get_revenue_growth(conn, symbol_id, decision_ts):
        return False
    
    # Condition (c): Stock 63-day return < SPY 63-day return - 10%
    stock_ret = compute_63day_return(conn, symbol_id, decision_ts)
    if stock_ret is None:
        return False
    spy_ret = compute_63day_return(conn, spy_id, decision_ts)
    if spy_ret is None:
        return False
    if stock_ret >= spy_ret - 0.10:
        return False
    
    # Condition (d): >=2 unique insiders purchased in [T-4, T] (business days)
    # T is decision_ts (filed_ts). Look back 5 business days.
    purchases = get_insider_purchases(conn, symbol_id, decision_ts)
    if not purchases:
        return False
    
    # Filter to [T-4biz, T]
    cutoff_ts = decision_ts - 5 * 24 * 3600  # approximate 5 calendar days
    unique_insiders = set()
    for insider, tx_ts, filed_ts in purchases:
        if filed_ts >= cutoff_ts and filed_ts <= decision_ts:
            unique_insiders.add(insider)
    if len(unique_insiders) < 2:
        return False
    
    # Condition (e): Disclosure delay <= 5 business days for the triggering trade
    # We need at least one trade in the window with delay <= 5 biz days
    has_timely = False
    for insider, tx_ts, filed_ts in purchases:
        if filed_ts >= cutoff_ts and filed_ts <= decision_ts:
            delay = business_days_between(tx_ts, filed_ts)
            if delay <= 5:
                has_timely = True
                break
    if not has_timely:
        return False
    
    return True

def get_forward_return_21d(conn, symbol_id, decision_ts):
    """Get 21-trading-day forward return from decision_ts (next bar open to 21 days later)"""
    # Get close at decision_ts (or next available bar)
    cur = conn.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
        ORDER BY ts LIMIT 22
    """, (symbol_id, decision_ts))
    rows = cur.fetchall()
    if len(rows) < 22:
        return None
    entry_close = rows[0][1]
    exit_close = rows[21][1]
    return (exit_close - entry_close) / entry_close

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    
    spy_id = get_spy_symbol_id(conn)
    if spy_id is None:
        print("INSUFFICIENT=1")
        return
    
    # Get date range from bars
    cur = conn.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf = '1d'")
    min_ts, max_ts = cur.fetchone()
    
    # We need data from 2018-07 onward per hypothesis
    start_epoch = date_to_epoch(datetime(2018, 7, 1).date())
    if min_ts > start_epoch:
        start_epoch = min_ts
    
    # Get eligible symbols
    eligible = get_eligible_symbols(conn, start_epoch)
    if not eligible:
        print("INSUFFICIENT=1")
        return
    
    # Collect all decision points: filed_ts of insider purchases for eligible symbols
    decision_points = []  # (symbol_id, filed_ts)
    for sym_id, _ in eligible:
        cur = conn.execute("""
            SELECT DISTINCT filed_ts FROM insider_trades
            WHERE symbol_id = ? AND code = 'P' AND filed_ts >= ? AND filed_ts <= ?
            ORDER BY filed_ts
        """, (sym_id, start_epoch, max_ts))
        for row in cur.fetchall():
            decision_points.append((sym_id, row[0]))
    
    if not decision_points:
        print("INSUFFICIENT=1")
        return
    
    # Sort by time
    decision_points.sort(key=lambda x: x[1])
    
    # Split: hold out most recent 20% as sealed era
    split_idx = int(len(decision_points) * 0.8)
    train_points = decision_points[:split_idx]
    sealed_points = decision_points[split_idx:]
    
    def evaluate(points, label):
        issued = 0
        hits = 0
        opportunities = 0
        issued_days = set()
        
        for sym_id, decision_ts in points:
            opportunities += 1
            
            # Check entry conditions
            if not check_entry_conditions(conn, sym_id, decision_ts, None, spy_id):
                continue
            
            # Get forward return label
            fwd_ret = get_forward_return_21d(conn, sym_id, decision_ts)
            if fwd_ret is None:
                continue
            
            # Predicted class: positive forward return (up)
            predicted_up = True  # hypothesis claims precision on "up" calls
            actual_up = fwd_ret > 0
            
            issued += 1
            issued_days.add(epoch_to_date(decision_ts))
            if actual_up:
                hits += 1
        
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class within issued
        distinct_days = len(issued_days)
        
        # Design effect: cluster by day, compute effective N
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use conservative rho=0.5, cluster by day
        day_counts = defaultdict(int)
        for sym_id, decision_ts in points:
            if check_entry_conditions(conn, sym_id, decision_ts, None, spy_id):
                fwd_ret = get_forward_return_21d(conn, sym_id, decision_ts)
                if fwd_ret is not None:
                    day_counts[epoch_to_date(decision_ts)] += 1
        
        if day_counts:
            avg_cluster = sum(day_counts.values()) / len(day_counts)
            design_effect = 1 + (avg_cluster - 1) * 0.5
            effective_n = issued / design_effect
        else:
            effective_n = 0.0
        
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_OPPORTUNITIES={opportunities}")
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.6f}")
        
        return issued, hits, opportunities, precision, base_rate, distinct_days, effective_n
    
    # Evaluate training era
    train_issued, train_hits, train_opp, train_prec, train_br, train_days, train_eff = evaluate(train_points, "TRAIN")
    
    # Evaluate sealed era
    sealed_issued, sealed_hits, sealed_opp, sealed_prec, sealed_br, sealed_days, sealed_eff = evaluate(sealed_points, "SEALED")
    
    # Overall (for final output)
    total_issued = train_issued + sealed_issued
    total_hits = train_hits + sealed_hits
    total_opp = train_opp + sealed_opp
    overall_prec = total_hits / total_issued if total_issued > 0 else 0.0
    overall_br = total_hits / total_issued if total_issued > 0 else 0.0
    all_issued_days = set()
    for sym_id, decision_ts in decision_points:
        if check_entry_conditions(conn, sym_id, decision_ts, None, spy_id):
            fwd_ret = get_forward_return_21d(conn, sym_id, decision_ts)
            if fwd_ret is not None:
                all_issued_days.add(epoch_to_date(decision_ts))
    total_distinct_days = len(all_issued_days)
    
    # Overall effective N
    day_counts = defaultdict(int)
    for sym_id, decision_ts in decision_points:
        if check_entry_conditions(conn, sym_id, decision_ts, None, spy_id):
            fwd_ret = get_forward_return_21d(conn, sym_id, decision_ts)
            if fwd_ret is not None:
                day_counts[epoch_to_date(decision_ts)] += 1
    if day_counts:
        avg_cluster = sum(day_counts.values()) / len(day_counts)
        design_effect = 1 + (avg_cluster - 1) * 0.5
        total_effective_n = total_issued / design_effect
    else:
        total_effective_n = 0.0
    
    # Print required output
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opp}")
    print(f"PRECISION={overall_prec:.6f}")
    print(f"BASE_RATE={overall_br:.6f}")
    print(f"DISTINCT_DAYS={total_distinct_days}")
    print(f"EFFECTIVE_N={total_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == "__main__":
    main()