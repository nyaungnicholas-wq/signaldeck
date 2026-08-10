import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True, timeout=10)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Step 1: Get candidate insider purchases (open-market purchase with prior sale by same insider)
    c.execute("""
        WITH purchases AS (
            SELECT symbol_id, insider, filed_ts, value,
                   DATE(filed_ts, 'unixepoch') as disclosure_day
            FROM insider_trades
            WHERE code = 'P' AND value >= 20000
        ),
        sales AS (
            SELECT symbol_id, insider, filed_ts
            FROM insider_trades
            WHERE code = 'S'
        )
        SELECT p.symbol_id, p.disclosure_day, MAX(p.value) as purchase_value,
               MIN(p.filed_ts) as purchase_filed_ts
        FROM purchases p
        WHERE EXISTS (
            SELECT 1 FROM sales s
            WHERE s.symbol_id = p.symbol_id
              AND s.insider = p.insider
              AND s.filed_ts < p.filed_ts
              AND s.filed_ts >= p.filed_ts - 15552000  -- 180 days in seconds
        )
        GROUP BY p.symbol_id, p.disclosure_day
    """)
    candidates = c.fetchall()
    
    if not candidates:
        print("INSUFFICIENT=1")
        return
    
    # Step 2: For each candidate, apply universe filters and compute forward return
    # Cache symbols data
    c.execute("SELECT id, active, delisted_at FROM symbols")
    symbols = {row['id']: row for row in c.fetchall()}
    
    # Cache daily bars per symbol (needed for multiple checks)
    bars_cache = {}
    
    issued = []
    opportunities = []
    
    for cand in candidates:
        sym_id = cand['symbol_id']
        disc_day_str = cand['disclosure_day']
        disc_ts = cand['purchase_filed_ts']
        purchase_value = cand['purchase_value']
        
        # Check symbol active and not delisted at disclosure
        sym = symbols.get(sym_id)
        if not sym or not sym['active']:
            continue
        if sym['delisted_at'] and sym['delisted_at'] <= disc_ts:
            continue
        
        # Get bars for symbol if not cached
        if sym_id not in bars_cache:
            c.execute("""
                SELECT ts, open, high, low, close, volume
                FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (sym_id,))
            bars_cache[sym_id] = c.fetchall()
        bars = bars_cache[sym_id]
        
        # Find bar for disclosure day (same date)
        disc_bar = None
        for bar in bars:
            bar_day = datetime.utcfromtimestamp(bar['ts']).strftime('%Y-%m-%d')
            if bar_day == disc_day_str:
                disc_bar = bar
                break
        if not disc_bar:
            continue
        
        close_disc = disc_bar['close']
        volume_disc = disc_bar['volume']
        if close_disc < 2.0:
            continue
        
        # Get previous 60 trading days before disclosure
        prev_bars = []
        for bar in bars:
            if bar['ts'] < disc_ts:
                prev_bars.append(bar)
            if len(prev_bars) > 60:
                prev_bars.pop(0)
        if len(prev_bars) < 60:
            continue
        
        # Median dollar volume over these 60 days
        dollar_vols = [bar['close'] * bar['volume'] for bar in prev_bars]
        dollar_vols.sort()
        n = len(dollar_vols)
        median_dollar_vol = (dollar_vols[n//2] + dollar_vols[(n-1)//2]) / 2 if n % 2 == 0 else dollar_vols[n//2]
        if median_dollar_vol < 1e6:
            continue
        
        # Check price not up more than 10% from close 20 days before
        if len(prev_bars) >= 20:
            close_20_before = prev_bars[-20]['close']
            if close_disc > 1.1 * close_20_before:
                continue
        
        # Compute forward return: next trading day's open to close 21 trading days later
        # Find next trading day after disclosure
        next_day = None
        next_day_idx = None
        for i, bar in enumerate(bars):
            if bar['ts'] > disc_ts:
                next_day = bar
                next_day_idx = i
                break
        if not next_day or next_day_idx is None:
            continue
        
        # Need 21 trading days after next day (i.e., index next_day_idx + 21)
        if next_day_idx + 21 >= len(bars):
            continue
        
        target_day = bars[next_day_idx + 21]
        fwd_return = (target_day['close'] / next_day['open']) - 1
        
        opportunities.append((sym_id, disc_day_str, disc_ts))
        
        # Issue call on first trading day after disclosure
        issued.append({
            'sym_id': sym_id,
            'disc_day': disc_day_str,
            'disc_ts': disc_ts,
            'fwd_return': fwd_return,
            'hit': fwd_return > 0
        })
    
    conn.close()
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Step 3: Split sample - most recent 20% by disclosure time
    issued.sort(key=lambda x: x['disc_ts'])
    split_idx