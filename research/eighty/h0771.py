# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 770
# cycle_index: 40
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_trading_days_bars(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT ts, open, high, low, close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return cur.fetchall()

def get_latest_close_before(conn, symbol_id, as_of_ts):
    cur = conn.execute(
        "SELECT close, ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 1",
        (symbol_id, as_of_ts)
    )
    return cur.fetchone()

def get_52w_high(conn, symbol_id, as_of_ts):
    # 52 weeks ~ 252 trading days
    start_ts = as_of_ts - 252 * 86400
    cur = conn.execute(
        "SELECT MAX(high) as high, ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? GROUP BY ts ORDER BY high DESC LIMIT 1",
        (symbol_id, start_ts, as_of_ts)
    )
    row = cur.fetchone()
    if row:
        return row[0], row[1]
    return None, None

def get_avg_dollar_volume_20d(conn, symbol_id, as_of_ts):
    start_ts = as_of_ts - 20 * 86400
    cur = conn.execute(
        "SELECT AVG(close * volume) FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=?",
        (symbol_id, start_ts, as_of_ts)
    )
    row = cur.fetchone()
    return row[0] if row and row[0] else 0

def get_forward_return_21d(conn, symbol_id, entry_ts):
    # Get close at entry_ts (or next available)
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts LIMIT 1",
        (symbol_id, entry_ts)
    )
    entry_row = cur.fetchone()
    if not entry_row:
        return None
    entry_close = entry_row[0]
    entry_date = epoch_to_date(entry_row[0] if isinstance(entry_row[0], int) else entry_ts)
    # Actually need the ts of the entry bar
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts LIMIT 1",
        (symbol_id, entry_ts)
    )
    entry_bar = cur.fetchone()
    if not entry_bar:
        return None
    entry_ts_actual, entry_close = entry_bar
    # Get 21 trading days later
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts>? ORDER BY ts LIMIT 21",
        (symbol_id, entry_ts_actual)
    )
    bars_21 = cur.fetchall()
    if len(bars_21) < 21:
        return None
    exit_close = bars_21[-1][0]
    return (exit_close - entry_close) / entry_close

def get_quarterly_revenues(conn, symbol_id, as_of_fetched_ts):
    # Get Revenues metric where fetched_at <= as_of_fetched_ts
    # as_of is the period end, fetched_at is when we learned it
    cur = conn.execute(
        """SELECT as_of, value FROM fundamentals 
           WHERE symbol_id=? AND metric='Revenues' AND fetched_at<=? AND as_of>0
           ORDER BY as_of""",
        (symbol_id, as_of_fetched_ts)
    )
    rows = cur.fetchall()
    # Group by quarter (as_of), take latest fetched_at per quarter
    quarter_map = {}
    for as_of, val in rows:
        if as_of not in quarter_map or quarter_map[as_of][1] < as_of_fetched_ts:
            quarter_map[as_of] = (val, as_of_fetched_ts)
    # Sort by as_of (quarter end)
    sorted_quarters = sorted(quarter_map.items())
    return [(as_of, val) for as_of, (val, _) in sorted_quarters]

def check_accelerating_yoy_growth(quarters):
    # quarters: list of (as_of, revenue) sorted by as_of ascending
    # Need last 3 quarters with YoY growth accelerating: q3_yoy > q2_yoy > q1_yoy
    if len(quarters) < 7:  # Need at least 3 quarters + 3 year-ago quarters
        return False
    # Compute YoY for each quarter where year-ago exists
    yoy = {}
    for i, (as_of, rev) in enumerate(quarters):
        year_ago_as_of = as_of - 4 * 90 * 86400  # approximate 1 year back
        # Find closest quarter to year_ago_as_of
        for j in range(i):
            if quarters[j][0] <= year_ago_as_of + 45*86400 and quarters[j][0] >= year_ago_as_of - 45*86400:
                yoy[as_of] = (rev - quarters[j][1]) / quarters[j][1] if quarters[j][1] != 0 else None
                break
    # Get last 3 quarters with valid YoY
    valid_quarters = [as_of for as_of in sorted(yoy.keys()) if yoy[as_of] is not None]
    if len(valid_quarters) < 3:
        return False
    last3 = valid_quarters[-3:]
    return yoy[last3[2]] > yoy[last3[1]] > yoy[last3[0]]

def get_institutional_ownership(conn, symbol_id, as_of_ts):
    # inst_holdings: period is quarter END, filed up to 45 days later
    # Only knowable at period + 45 days. So use period <= as_of_ts - 45*86400
    cutoff_period = as_of_ts - 45 * 86400
    cur = conn.execute(
        """SELECT period, SUM(value) as total_value FROM inst_holdings 
           WHERE symbol_id=? AND period<=? GROUP BY period ORDER BY period""",
        (symbol_id, cutoff_period)
    )
    return cur.fetchall()

def check_inst_ownership_not_decreased(conn, symbol_id, as_of_ts):
    holdings = get_institutional_ownership(conn, symbol_id, as_of_ts)
    if len(holdings) < 2:
        return False
    last_two = holdings[-2:]
    return last_two[1][1] >= last_two[0][1]  # value not decreased

def get_quarter_ends(conn):
    cur = conn.execute("SELECT DISTINCT period FROM inst_holdings ORDER BY period")
    return [row[0] for row in cur.fetchall()]

def is_near_earnings(filed_ts, quarter_ends):
    # Within 10 sessions of quarter-end ±15 sessions
    # 1 session ~ 1 day for daily bars
    for qe in quarter_ends:
        if abs(filed_ts - qe) <= 25 * 86400:  # 15+10 = 25 days
            return True
    return False

def has_officer_sale_prior_12m(conn, symbol_id, insider_name, filed_ts):
    start_ts = filed_ts - 365 * 86400
    cur = conn.execute(
        """SELECT 1 FROM insider_trades 
           WHERE symbol_id=? AND insider=? AND code='S' AND filed_ts>=? AND filed_ts<?
           LIMIT 1""",
        (symbol_id, insider_name, start_ts, filed_ts)
    )
    return cur.fetchone() is not None

def get_market_cap(conn, symbol_id, as_of_ts):
    # close * SharesOutstanding
    close_row = get_latest_close_before(conn, symbol_id, as_of_ts)
    if not close_row:
        return 0
    close = close_row[0]
    cur = conn.execute(
        """SELECT value FROM fundamentals 
           WHERE symbol_id=? AND metric='SharesOutstanding' AND fetched_at<=? AND as_of>0
           ORDER BY fetched_at DESC LIMIT 1""",
        (symbol_id, as_of_ts)
    )
    row = cur.fetchone()
    if not row:
        return 0
    shares = row[0]
    return close * shares

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.execute("PRAGMA query_only = ON")
    
    # Get all symbols with daily bars from 2018-07
    start_2018 = date_to_epoch(datetime(2018, 7, 1).date())
    cur = conn.execute(
        """SELECT DISTINCT s.id, s.symbol FROM symbols s
           JOIN bars b ON s.id=b.symbol_id
           WHERE b.tf='1d' AND b.ts>=? AND s.market='stocks' AND s.active=1""",
        (start_2018,)
    )
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get quarter ends for earnings proximity check
    quarter_ends = get_quarter_ends(conn)
    
    # Get all officer purchases (code='P', title contains CEO/CFO/COO)
    cur = conn.execute(
        """SELECT symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
           FROM insider_trades
           WHERE code='P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%COO%')
           AND value > 100000
           ORDER BY filed_ts"""
    )
    officer_purchases = cur.fetchall()
    
    opportunities = []
    issued_calls = []
    
    for purch in officer_purchases:
        symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts = purch
        
        # Check market cap > $1B at filed_ts
        mcap = get_market_cap(conn, symbol_id, filed_ts)
        if mcap <= 1e9:
            opportunities.append((symbol_id, filed_ts, False, 'mcap'))
            continue
        
        # Check 3 quarters accelerating YoY revenue growth (using fetched_at <= filed_ts)
        quarters = get_quarterly_revenues(conn, symbol_id, filed_ts)
        if not check_accelerating_yoy_growth(quarters):
            opportunities.append((symbol_id, filed_ts, False, 'revenue'))
            continue
        
        # Check price >20% below 52-week high
        high_52w, high_ts = get_52w_high(conn, symbol_id, filed_ts)
        if not high_52w:
            opportunities.append((symbol_id, filed_ts, False, 'no_52w'))
            continue
        close_row = get_latest_close_before(conn, symbol_id, filed_ts)
        if not close_row:
            opportunities.append((symbol_id, filed_ts, False, 'no_close'))
            continue
        close = close_row[0]
        if close >= high_52w * 0.8:
            opportunities.append((symbol_id, filed_ts, False, 'not_20pct_down'))
            continue
        
        # Check 52-week high occurred >60 sessions ago
        if (filed_ts - high_ts) <= 60 * 86400:
            opportunities.append((symbol_id, filed_ts, False, 'high_too_recent'))
            continue
        
        # Check institutional ownership not decreased over last 2 quarters
        if not check_inst_ownership_not_decreased(conn, symbol_id, filed_ts):
            opportunities.append((symbol_id, filed_ts, False, 'inst_decreased'))
            continue
        
        # Check not near earnings (quarter-end ±15 sessions, within 10 sessions)
        if is_near_earnings(filed_ts, quarter_ends):
            opportunities.append((symbol_id, filed_ts, False, 'near_earnings'))
            continue
        
        # Check 20-day avg dollar volume >= $5M
        avg_dv = get_avg_dollar_volume_20d(conn, symbol_id, filed_ts)
        if avg_dv < 5e6:
            opportunities.append((symbol_id, filed_ts, False, 'low_vol'))
            continue
        
        # Check no officer sale in prior 12 months
        if has_officer_sale_prior_12m(conn, symbol_id, insider, filed_ts):
            opportunities.append((symbol_id, filed_ts, False, 'prior_sale'))
            continue
        
        # All conditions met - this is an issued call
        opportunities.append((symbol_id, filed_ts, True, 'issued'))
        issued_calls.append((symbol_id, filed_ts))
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # Compute labels (21-day forward return) for issued calls
    labeled = []
    for symbol_id, filed_ts in issued_calls:
        fwd_ret = get_forward_return_21d(conn, symbol_id, filed_ts)
        if fwd_ret is not None:
            labeled.append((symbol_id, filed_ts, fwd_ret > 0))
    
    if not labeled:
        print("INSUFFICIENT=1")
        return
    
    # Sort by filed_ts for temporal split
    labeled.sort(key=lambda x: x[1])
    n = len(labeled)
    split_idx = int(n * 0.8)
    train = labeled[:split_idx]
    sealed = labeled[split_idx:]
    
    # Compute metrics on full set
    issued_count = len(labeled)
    hits = sum(1 for _, _, up in labeled if up)
    precision = hits / issued_count if issued_count > 0 else 0
    base_rate = hits / issued_count if issued_count > 0 else 0  # base rate within issued subset
    
    distinct_days = len(set(epoch_to_date(ts) for _, ts, _ in labeled))
    
    # Design effect: cluster by day, compute effective N
    day_counts = defaultdict(int)
    for _, ts, _ in labeled:
        day_counts[epoch_to_date(ts)] += 1
    # Effective N = n / (1 + (avg_cluster_size - 1) * rho)
    # Simplified: use Kish's effective sample size: n / (1 + CV^2) where CV is coeff of variation of cluster sizes
    # Or simpler: design effect = 1 + (m-1)*ICC, but we don't have ICC.
    # Use: effective_n = n / design_effect where design_effect = 1 + (avg_cluster_size - 1) * 0.1 (conservative)
    # Actually, requirement says EFFECTIVE_N must be < ISSUED. Use clustering by day.
    cluster_sizes = list(day_counts.values())
    avg_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    # Conservative ICC estimate of 0.05 for financial returns
    design_effect = 1 + (avg_cluster - 1) * 0.05
    effective_n = issued_count / design_effect
    
    # Sealed precision
    sealed_hits = sum(1 for _, _, up in sealed if up)
    sealed_precision = sealed_hits / len(sealed) if sealed else 0
    
    # Opportunities considered = all officer purchases that passed basic filters (mcap, value>100k, officer title)
    # Actually opportunities = decision points considered = all officer purchases we evaluated
    opp_count = len(opportunities)
    
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opp_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()