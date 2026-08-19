# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 844
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

MECHANISM = "Officers step in as liquidity providers on high-volume trade days when selling pressure is extreme, acquiring shares at favorable prices that lead to outsized returns."
HORIZON = "63-session forward return from bars (tf='1d')"
UNIVERSE = "stocks market, active symbols, avg daily dollar volume > $10M over prior 252 sessions"
ENTRY = "An officer (title contains 'CEO' or 'CFO') executes an open-market purchase (code='P') at trade date tx_ts where the day's volume exceeds the prior 20-session average volume by >100%, and the trade date is a valid trading session."
ABSTAIN = "Volume condition not met, or not officer, or code != 'P', or symbol not in universe, or trade date within 10 sessions of prior officer trade date for same symbol."
CLAIM = "Precision of positive 63-session forward returns exceeds base rate by at least 15 percentage points."

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def get_universe_symbols(conn):
    cur = conn.execute("""
        SELECT id FROM symbols 
        WHERE market = 'stocks' AND active = 1
    """)
    return [row[0] for row in cur.fetchall()]

def get_daily_bars(conn, symbol_id):
    cur = conn.execute("""
        SELECT ts, close, volume FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (symbol_id,))
    return cur.fetchall()

def get_officer_trades(conn, symbol_id):
    cur = conn.execute("""
        SELECT tx_ts, filed_ts, title, code, shares, price, value
        FROM insider_trades
        WHERE symbol_id = ? AND code = 'P'
        ORDER BY tx_ts
    """, (symbol_id,))
    return cur.fetchall()

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    return 'CEO' in t or 'CFO' in t or 'CHIEF EXECUTIVE' in t or 'CHIEF FINANCIAL' in t

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def find_bar_index(bars, target_ts):
    for i, (ts, _, _) in enumerate(bars):
        if ts == target_ts:
            return i
    return -1

def find_bar_for_date(bars, target_date):
    target_ts = int(datetime.combine(target_date, datetime.min.time(), tzinfo=timezone.utc).timestamp())
    return find_bar_index(bars, target_ts)

def compute_metrics(entries, sealed_start_idx):
    if not entries:
        return None
    
    train_entries = entries[:sealed_start_idx]
    sealed_entries = entries[sealed_start_idx:]
    
    def calc(entries_subset):
        if not entries_subset:
            return 0, 0, 0
        issued = len(entries_subset)
        hits = sum(1 for e in entries_subset if e['label'] == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = precision
        distinct_days = len(set(e['trade_date'] for e in entries_subset))
        return issued, hits, precision, base_rate, distinct_days
    
    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days = calc(train_entries)
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct_days = calc(sealed_entries)
    
    total_issued = train_issued + sealed_issued
    total_hits = train_hits + sealed_hits
    overall_precision = total_hits / total_issued if total_issued > 0 else 0
    overall_base_rate = overall_precision
    overall_distinct_days = len(set(e['trade_date'] for e in entries))
    
    design_effect = 1.0
    if overall_distinct_days > 0:
        design_effect = total_issued / overall_distinct_days
    effective_n = total_issued / design_effect if design_effect > 0 else 0
    
    return {
        'issued': total_issued,
        'opportunities': len(entries),
        'precision': overall_precision,
        'base_rate': overall_base_rate,
        'distinct_days': overall_distinct_days,
        'effective_n': effective_n,
        'sealed_precision': sealed_precision if sealed_issued > 0 else 0
    }

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row
    
    symbols = get_universe_symbols(conn)
    if not symbols:
        print("INSUFFICIENT=1")
        return 0
    
    all_entries = []
    opportunities = 0
    
    for symbol_id in symbols:
        bars = get_daily_bars(conn, symbol_id)
        if len(bars) < 252 + 63 + 20:
            continue
        
        ts_to_idx = {ts: i for i, (ts, _, _) in enumerate(bars)}
        dates = [datetime.fromtimestamp(ts, tz=timezone.utc).date() for ts, _, _ in bars]
        
        trades = get_officer_trades(conn, symbol_id)
        officer_trades = [t for t in trades if is_officer(t['title'])]
        
        last_trade_idx = -20
        
        for trade in officer_trades:
            opportunities += 1
            tx_ts = trade['tx_ts']
            trade_date = ts_to_date(tx_ts)
            
            bar_idx = find_bar_for_date(bars, trade_date)
            if bar_idx == -1:
                continue
            
            if bar_idx < 252 + 20:
                continue
            
            if bar_idx + 63 >= len(bars):
                continue
            
            if bar_idx - last_trade_idx < 10:
                continue
            
            vol_20_avg = sum(bars[bar_idx - 20 + j][2] for j in range(20)) / 20
            day_vol = bars[bar_idx][2]
            
            if day_vol <= 2 * vol_20_avg:
                continue
            
            dollar_vol_252 = sum(bars[bar_idx - 252 + j][1] * bars[bar_idx - 252 + j][2] for j in range(252)) / 252
            if dollar_vol_252 <= 10_000_000:
                continue
            
            entry_close = bars[bar_idx][1]
            exit_close = bars[bar_idx + 63][1]
            fwd_return = (exit_close - entry_close) / entry_close
            label = 1 if fwd_return > 0 else 0
            
            all_entries.append({
                'symbol_id': symbol_id,
                'trade_date': trade_date,
                'tx_ts': tx_ts,
                'label': label,
                'fwd_return': fwd_return
            })
            last_trade_idx = bar_idx
    
    if not all_entries:
        print("INSUFFICIENT=1")
        return 0
    
    all_entries.sort(key=lambda x: x['tx_ts'])
    
    sealed_start_idx = int(len(all_entries) * 0.8)
    
    metrics = compute_metrics(all_entries, sealed_start_idx)
    
    print(f"ISSUED={metrics['issued']}")
    print(f"OPPORTUNITIES={metrics['opportunities']}")
    print(f"PRECISION={metrics['precision']:.6f}")
    print(f"BASE_RATE={metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={metrics['distinct_days']}")
    print(f"EFFECTIVE_N={metrics['effective_n']:.2f}")
    print(f"SEALED_PRECISION={metrics['sealed_precision']:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())