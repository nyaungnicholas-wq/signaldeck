# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 733
# cycle_index: 3
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

def fetch_macro_spread(conn):
    cur = conn.execute("SELECT ts, value FROM macro_series WHERE series IN ('DGS10','DGS2') ORDER BY series, ts")
    rows = cur.fetchall()
    dgs10 = {}
    dgs2 = {}
    for ts, val, series in [(r[0], r[1], 'DGS10') for r in rows if r[0] is not None]:
        pass
    for ts, val in rows:
        pass
    series_map = defaultdict(dict)
    for ts, val in rows:
        pass
    cur = conn.execute("SELECT series, ts, value FROM macro_series WHERE series IN ('DGS10','DGS2') ORDER BY series, ts")
    dgs10 = {}
    dgs2 = {}
    for series, ts, val in cur.fetchall():
        if series == 'DGS10':
            dgs10[ts] = val
        else:
            dgs2[ts] = val
    common_ts = sorted(set(dgs10.keys()) & set(dgs2.keys()))
    spread = {}
    for ts in common_ts:
        spread[ts] = dgs10[ts] - dgs2[ts]
    return spread

def compute_steepening(spread, ts, lookback_days=63):
    if ts not in spread:
        return False
    target_ts = ts - lookback_days * 86400
    past_ts = [s for s in spread if s <= target_ts]
    if not past_ts:
        return False
    past_ts = max(past_ts)
    return spread[ts] > spread[past_ts]

def fetch_sentiment(conn):
    cur = conn.execute("SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day")
    by_symbol = defaultdict(list)
    for sym, day, score in cur.fetchall():
        by_symbol[sym].append((day, score))
    return by_symbol

def sentiment_condition(sent_list, day_str, window_short=5, window_long=60):
    idx = -1
    for i, (d, _) in enumerate(sent_list):
        if d == day_str:
            idx = i
            break
    if idx == -1:
        return False
    if idx < window_long - 1:
        return False
    short_scores = [s for _, s in sent_list[idx-window_short+1:idx+1]]
    long_scores = [s for _, s in sent_list[idx-window_long+1:idx+1]]
    return (sum(short_scores)/len(short_scores)) < (sum(long_scores)/len(long_scores))

def fetch_insider_purchases(conn):
    cur = conn.execute("""
        SELECT symbol_id, filed_ts FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL
        ORDER BY symbol_id, filed_ts
    """)
    by_symbol = defaultdict(list)
    for sym, ft in cur.fetchall():
        by_symbol[sym].append(ft)
    return by_symbol

def fetch_bars(conn, symbol_id):
    cur = conn.execute("SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts", (symbol_id,))
    return cur.fetchall()

def get_forward_return(bars, decision_ts, horizon=21):
    idx = -1
    for i, (ts, _) in enumerate(bars):
        if ts <= decision_ts:
            idx = i
        else:
            break
    if idx == -1 or idx + horizon >= len(bars):
        return None
    close_now = bars[idx][1]
    close_fwd = bars[idx + horizon][1]
    return (close_fwd - close_now) / close_now

def utc_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def main():
    conn = connect()
    
    print("Loading macro spread...", file=sys.stderr)
    spread = fetch_macro_spread(conn)
    if not spread:
        print("INSUFFICIENT=1")
        return
    
    print("Loading sentiment...", file=sys.stderr)
    sentiment = fetch_sentiment(conn)
    
    print("Loading insider purchases...", file=sys.stderr)
    insider = fetch_insider_purchases(conn)
    
    print("Loading bars...", file=sys.stderr)
    bars_cache = {}
    for sym in insider:
        bars_cache[sym] = fetch_bars(conn, sym)
    
    opportunities = []
    issued = []
    
    for sym, purchases in insider.items():
        if sym not in sentiment or sym not in bars_cache:
            continue
        sent_list = sentiment[sym]
        bars = bars_cache[sym]
        if not bars:
            continue
        
        recent_purchase_days = set()
        for ft in purchases:
            day = utc_date(ft)
            day_str = day.isoformat()
            
            if day in recent_purchase_days:
                continue
            
            has_data = True
            has_data &= compute_steepening(spread, ft)
            has_data &= sentiment_condition(sent_list, day_str)
            has_data &= get_forward_return(bars, ft) is not None
            
            if not has_data:
                recent_purchase_days.add(day)
                continue
            
            fwd_ret = get_forward_return(bars, ft)
            actual_up = 1 if fwd_ret > 0 else 0
            
            steep = compute_steepening(spread, ft)
            sent_cond = sentiment_condition(sent_list, day_str)
            no_recent = day not in recent_purchase_days
            
            if steep and sent_cond and no_recent:
                issued.append((ft, day, actual_up))
            
            opportunities.append((ft, day, actual_up, steep and sent_cond and no_recent))
            recent_purchase_days.add(day)
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    opportunities.sort(key=lambda x: x[0])
    issued.sort(key=lambda x: x[0])
    
    split_idx = int(len(opportunities) * 0.8)
    sealed_opps = opportunities[split_idx:]
    sealed_issued = [i for i in issued if i[0] >= sealed_opps[0][0]] if sealed_opps else []
    
    train_opps = opportunities[:split_idx]
    train_issued = [i for i in issued if i[0] < sealed_opps[0][0]] if sealed_opps else issued
    
    def compute_metrics(ops, iss):
        if not iss:
            return 0, 0, 0, 0, 0, 0
        issued_count = len(iss)
        opp_count = len(ops)
        hits = sum(1 for _, _, up in iss if up == 1)
        precision = hits / issued_count if issued_count else 0
        base_rate = sum(1 for _, _, up, _ in ops if up == 1) / opp_count if opp_count else 0
        distinct_days = len(set(day for _, day, _ in iss))
        weekly_counts = defaultdict(int)
        for _, day, _ in iss:
            week_start = day - timedelta(days=day.weekday())
            weekly_counts[week_start] += 1
        counts = list(weekly_counts.values())
        if len(counts) > 1:
            mean_c = sum(counts) / len(counts)
            var_c = sum((c - mean_c)**2 for c in counts) / len(counts)
            design_effect = max(1.01, var_c / mean_c if mean_c > 0 else 1.01)
        else:
            design_effect = 1.01
        effective_n = issued_count / design_effect
        return issued_count, opp_count, precision, base_rate, distinct_days, effective_n
    
    train_issued_c, train_opp_c, train_prec, train_base, train_days, train_eff = compute_metrics(train_opps, train_issued)
    sealed_issued_c, sealed_opp_c, sealed_prec, sealed_base, sealed_days, sealed_eff = compute_metrics(sealed_opps, sealed_issued)
    
    print(f"ISSUED={train_issued_c}")
    print(f"OPPORTUNITIES={train_opp_c}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_base:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()