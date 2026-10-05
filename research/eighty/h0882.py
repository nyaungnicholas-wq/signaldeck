# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 881
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check critical data availability
    # 1. Fundamentals: need EPS/Revenue with historical as_of, fetched_at <= decision dates
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id) as syms, MIN(fetched_at) as min_fetch, MAX(fetched_at) as max_fetch,
               MIN(as_of) as min_asof, MAX(as_of) as max_asof
        FROM fundamentals WHERE metric IN ('EPS','Revenues') AND as_of > 0
    """)
    fund = cur.fetchone()
    if not fund or fund['syms'] == 0:
        print("INSUFFICIENT=1"); return

    # 2. Sentiment features: need daily mean_score, n_all for slope/acceleration/coverage
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id) as syms, MIN(day) as min_day, MAX(day) as max_day
        FROM sentiment_features
    """)
    sent = cur.fetchone()
    if not sent or sent['syms'] == 0:
        print("INSUFFICIENT=1"); return

    # 3. Bars: need daily close, volume for returns, market cap proxy, avg dollar volume
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id) as syms, MIN(ts) as min_ts, MAX(ts) as max_ts
        FROM bars WHERE tf = '1d'
    """)
    bars = cur.fetchone()
    if not bars or bars['syms'] == 0:
        print("INSUFFICIENT=1"); return

    # 4. Insider trades: need filed_ts for sales detection
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id) as syms, MIN(filed_ts) as min_ft, MAX(filed_ts) as max_ft
        FROM insider_trades WHERE code = 'S'
    """)
    insider = cur.fetchone()

    # 5. Symbols: need active symbols, market cap proxy requires shares outstanding
    cur.execute("SELECT COUNT(*) as n FROM symbols WHERE active = 1")
    sym_count = cur.fetchone()['n']

    # Check date overlap for 21-day horizon
    # Bars max_ts ~ 2026-08-16 (unix). Need 21 trading days ~ 30 calendar days before for forward return.
    # Latest decision ~ 2026-07-17. Fundamentals fetched_at starts 2026-07-06.
    # Overlap window: ~10 trading days. Need 2+ quarters acceleration -> need 3+ quarters per symbol.
    # With fetched_at all in Jul-Aug 2026, as-of discipline means NO fundamental data before 2026-07-06.
    # Decision window with fundamentals: 2026-07-06 to 2026-07-17 (10 days).
    # 44 distinct label days in prediction_outcomes but only ~10 with fundamentals.
    # Need 3+ quarters per symbol -> check if as_of provides historical quarters.
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id) as syms, COUNT(*) as rows,
               MIN(as_of) as min_asof, MAX(as_of) as max_asof
        FROM fundamentals WHERE metric IN ('EPS','Revenues') AND as_of > 0
    """)
    fund_hist = cur.fetchone()

    # If as_of range doesn't span multiple quarters, insufficient
    if not fund_hist or fund_hist['rows'] < 3 * 100:  # rough check
        print("INSUFFICIENT=1"); return

    # No sector data to exclude financials -> INSUFFICIENT per hypothesis requirement
    cur.execute("PRAGMA table_info(symbols)")
    cols = [r[1] for r in cur.fetchall()]
    if 'sector' not in cols and 'industry' not in cols:
        print("INSUFFICIENT=1"); return

    # Market cap > $500M needs shares outstanding * price. SharesOutstanding only in fundamentals (recent fetch).
    # Avg daily volume > $1M needs dollar volume history.
    # News coverage median per symbol needs sufficient history.

    # Given the severe constraints (no sector, fundamentals fetched_at window too narrow for as-of discipline,
    # 21-day horizon needs bars beyond label window, <15 decision days with fundamentals),
    # the hypothesis cannot be validly tested.
    print("INSUFFICIENT=1")

if __name__ == '__main__':
    main()