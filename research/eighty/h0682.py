# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 681
# cycle_index: 8
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

def get_trading_days(conn):
    cur = conn.execute("SELECT DISTINCT date(ts, 'unixepoch') as day FROM bars WHERE tf='1d' ORDER BY day")
    return [row[0] for row in cur.fetchall()]

def get_yield_curve(conn):
    cur = conn.execute("SELECT ts, value FROM macro_series WHERE series='T10Y2Y' ORDER BY ts")
    rows = cur.fetchall()
    if not rows:
        return None
    yc = {}
    for ts, val in rows:
        day = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        yc[day] = val
    return yc

def fill_yield_curve(yc_dict, trading_days):
    filled = {}
    last_val = None
    for day in trading_days:
        if day in yc_dict:
            last_val = yc_dict[day]
        if last_val is not None:
            filled[day] = last_val
    return filled

def get_sentiment(conn):
    cur = conn.execute("SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day")
    rows = cur.fetchall()
    sent = defaultdict(dict)
    for sid, day, score in rows:
        sent[sid][day] = score
    return sent

def get_insider_purchases(conn):
    cur = conn.execute("""
        SELECT symbol_id, filed_ts, shares, price, shares*price as value, insider
        FROM insider_trades
        WHERE code='P' AND shares>0 AND price>0
        ORDER BY filed_ts
    """)
    return cur.fetchall()

def get_bars_for_return(conn, symbol_id, entry_day, exit_day):
    cur = conn.execute("""
        SELECT open FROM bars WHERE symbol_id=? AND tf='1d' AND date(ts,'unixepoch')=?
    """, (symbol_id, entry_day))
    row = cur.fetchone()
    if not row:
        return None
    entry_open = row[0]
    cur = conn.execute("""
        SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND date(ts,'unixepoch')=?
    """, (symbol_id, exit_day))
    row = cur.fetchone()
    if not row:
        return None
    exit_close = row[0]
    return (exit_close - entry_open) / entry_open

def next_trading_day(trading_days, day):
    try:
        idx = trading_days.index(day)
        if idx + 1 < len(trading_days):
            return trading_days[idx + 1]
    except ValueError:
        pass
    return None

def nth_trading_day_after(trading_days, start_day, n):
    try:
        idx = trading_days.index(start_day)
        if idx + n < len(trading_days):
            return trading_days[idx + n]
    except ValueError:
        pass
    return None

def main():
    conn = connect()
    
    trading_days = get_trading_days(conn)
    if not trading_days:
        print("INSUFFICIENT=1")
        return 0
    
    yc_raw = get_yield_curve(conn)
    if yc_raw is None:
        print("INSUFFICIENT=1")
        return 0
    yc_filled = fill_yield_curve(yc_raw, trading_days)
    
    sentiment = get_sentiment(conn)
    insider_trades = get_insider_purchases(conn)
    
    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0
    
    insider_history = defaultdict(list)
    opportunities = 0
    issued = 0
    hits = 0
    sealed_hits = 0
    sealed_issued = 0
    issued_days = set()
    issued_symbol_days = []
    
    decision_dates = []
    for symbol_id, filed_ts, shares, price, value, insider in insider_trades:
        decision_day = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        decision_dates.append(decision_day)
    
    if not decision_dates:
        print("INSUFFICIENT=1")
        return 0
    
    decision_dates_sorted = sorted(set(decision_dates))
    split_idx = int(len(decision_dates_sorted) * 0.8)
    sealed_cutoff = decision_dates_sorted[split_idx] if split_idx < len(decision_dates_sorted) else decision_dates_sorted[-1]
    
    for symbol_id, filed_ts, shares, price, value, insider in insider_trades:
        decision_day = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        
        if decision_day not in trading_days:
            continue
        
        hist = insider_history[(symbol_id, insider)]
        if len(hist) >= 5:
            hist_sorted = sorted(hist)
            p75 = hist_sorted[int(len(hist_sorted) * 0.75)]
            large_vs_history = value >= p75
        else:
            large_vs_history = False
        
        insider_history[(symbol_id, insider)].append(value)
        
        if not large_vs_history:
            continue
        
        sent_data = sentiment.get(symbol_id, {})
        sent_days = sorted([d for d in sent_data.keys() if d < decision_day])
        if len(sent_days) < 5:
            continue
        last5 = sent_days[-5:]
        scores = [sent_data[d] for d in last5]
        if scores[0] >= -0.1:
            continue
        improving = all(scores[i] <= scores[i+1] for i in range(4))
        if not improving:
            continue
        
        yc_days = [d for d in trading_days if d < decision_day and d in yc_filled]
        if len(yc_days) < 20:
            continue
        last20 = yc_days[-20:]
        yc_vals = [yc_filled[d] for d in last20]
        steepening = yc_vals[-1] > yc_vals[0]
        if not steepening:
            continue
        
        opportunities += 1
        
        entry_day = next_trading_day(trading_days, decision_day)
        if not entry_day:
            continue
        exit_day = nth_trading_day_after(trading_days, entry_day, 20)
        if not exit_day:
            continue
        
        fwd_ret = get_bars_for_return(conn, symbol_id, entry_day, exit_day)
        if fwd_ret is None:
            continue
        
        hit = 1 if fwd_ret > 0 else 0
        is_sealed = decision_day >= sealed_cutoff
        
        issued += 1
        hits += hit
        issued_days.add(decision_day)
        issued_symbol_days.append((symbol_id, decision_day))
        
        if is_sealed:
            sealed_issued += 1
            sealed_hits += hit
    
    if issued == 0:
        print("INSUFFICIENT=1")
        return 0
    
    precision = hits / issued
    base_rate = hits / issued
    distinct_days = len(issued_days)
    
    day_counts = defaultdict(int)
    for _, day in issued_symbol_days:
        day_counts[day] += 1
    avg_per_day = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    design_effect = 1 + (avg_per_day - 1) * 0.3
    effective_n = issued / design_effect
    
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    return 0

if __name__ == '__main__':
    sys.exit(main())