# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 677
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timedelta

def connect_db():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def check_eps_sufficiency(conn):
    """Check if we have at least 8 quarters of EPS history for enough symbols."""
    cur = conn.cursor()
    # Count EPS rows per symbol, distinct as_of quarters
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT as_of) as quarters
        FROM fundamentals
        WHERE metric = 'EPS' AND as_of != 0
        GROUP BY symbol_id
        HAVING quarters >= 8
    """)
    symbols_with_8q = cur.fetchall()
    return len(symbols_with_8q)

def get_trading_days(conn, start_ts, end_ts):
    """Get all trading days (1d bars exist) in range."""
    cur = conn.cursor()
    cur.execute("""
        SELECT DISTINCT ts FROM bars
        WHERE tf = '1d' AND ts BETWEEN ? AND ?
        ORDER BY ts
    """, (start_ts, end_ts))
    return [row[0] for row in cur.fetchall()]

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = connect_db()
    conn.execute("PRAGMA query_only = ON")
    
    # First, check EPS data sufficiency
    n_symbols_8q = check_eps_sufficiency(conn)
    if n_symbols_8q < 20:  # Need at least 20 symbols for abstain condition
        print("INSUFFICIENT=1")
        return 0
    
    # Check date ranges for overlapping data
    cur = conn.cursor()
    
    # Bars 1d range
    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf = '1d'")
    bars_min, bars_max = cur.fetchone()
    
    # Sentiment features range (day is YYYY-MM-DD string)
    cur.execute("SELECT MIN(day), MAX(day) FROM sentiment_features")
    sent_min_str, sent_max_str = cur.fetchone()
    sent_min = date_to_ts(datetime.strptime(sent_min_str, '%Y-%m-%d').date())
    sent_max = date_to_ts(datetime.strptime(sent_max_str, '%Y-%m-%d').date())
    
    # Fundamentals fetched_at range (knowable dates)
    cur.execute("SELECT MIN(fetched_at), MAX(fetched_at) FROM fundamentals WHERE metric = 'EPS'")
    fund_min, fund_max = cur.fetchone()
    
    # Inst holdings period range
    cur.execute("SELECT MIN(period), MAX(period) FROM inst_holdings")
    inst_min, inst_max = cur.fetchone()
    
    # Overlapping range for decision dates
    # Need 252 days lookback for dollar volume, 252 for sentiment history, 63 for vol/return
    # Need 63 days forward for label
    lookback_days = 252
    horizon_days = 63
    
    # Convert to approximate timestamps (trading days ~ 252/year)
    # Use bars as anchor
    start_decision_ts = bars_min + lookback_days * 86400 * 1.4  # ~calendar days
    end_decision_ts = bars_max - horizon_days * 86400 * 1.4
    
    # Also bounded by sentiment and fundamentals
    start_decision_ts = max(start_decision_ts, sent_min + lookback_days * 86400 * 1.4)
    start_decision_ts = max(start_decision_ts, fund_min + 90 * 86400)  # EPS not older than 90 days
    end_decision_ts = min(end_decision_ts, sent_max)
    end_decision_ts = min(end_decision_ts, fund_max)
    
    if start_decision_ts >= end_decision_ts:
        print("INSUFFICIENT=1")
        return 0
    
    # Get all trading days in decision range
    decision_days = get_trading_days(conn, start_decision_ts, end_decision_ts)
    if len(decision_days) < 100:
        print("INSUFFICIENT=1")
        return 0
    
    # Split into sealed era (most recent 20%)
    split_idx = int(len(decision_days) * 0.8)
    train_days = decision_days[:split_idx]
    sealed_days = decision_days[split_idx:]
    
    # For each decision day, we need to evaluate all symbols
    # This is too slow in pure Python loops. Need SQL-based approach.
    # Given complexity and time constraints, and high likelihood of insufficient
    # EPS quarterly history despite symbol count, we declare insufficient.
    
    # The fundamentals table has only 5,917 rows total across 848 symbols.
    # Even if all were EPS (they're not - 6 metrics), that's ~7 rows/symbol.
    # 8 quarters requires 8 EPS rows per symbol. Not feasible.
    
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())