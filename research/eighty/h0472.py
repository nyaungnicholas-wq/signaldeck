# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 471
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def load_symbols(conn):
    cur = conn.execute("SELECT id, symbol FROM symbols WHERE active=1 AND market='stocks'")
    return {row[0]: row[1] for row in cur.fetchall()}

def load_stocktwits(conn):
    cur = conn.execute("SELECT symbol_id, ts, bullish, bearish, untagged, total FROM stocktwits_sentiment")
    data = defaultdict(list)
    for sid, ts, bull, bear, untagged, total in cur.fetchall():
        data[sid].append((ts, total))
    for sid in data:
        data[sid].sort()
    return data

def load_news_sentiment(conn):
    cur = conn.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
    data = defaultdict(dict)
    for sid, day, score in cur.fetchall():
        data[sid][day] = score
    return data

def load_bars_1d(conn):
    cur = conn.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    data = defaultdict(list)
    for sid, ts, close in cur.fetchall():
        data[sid].append((ts, close))
    return data

def load_shares_outstanding(conn):
    cur = conn.execute("SELECT symbol_id, value, as_of, fetched_at FROM fundamentals WHERE metric='SharesOutstanding'")
    data = defaultdict(list)
    for sid, val, as_of, fetched in cur.fetchall():
        try:
            data[sid].append((int(as_of), float(val), int(fetched)))
        except:
            pass
    for sid in data:
        data[sid].sort(key=lambda x: x[2])
    return data

def get_shares_asof(shares_data, symbol_id, asof_ts):
    if symbol_id not in shares_data:
        return None
    best = None
    for as_of, val, fetched in shares_data[symbol_id]:
        if fetched <= asof_ts:
            best = val
        else:
            break
    return best

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_ts(day_str):
    return int(datetime.strptime(day_str, '%Y-%m-%d').timestamp())

def compute_returns(bars, horizon_days=5):
    returns = defaultdict(dict)
    for sid, series in bars.items():
        closes = [c for _, c in series]
        tss = [t for t, _ in series]
        n = len(closes)
        for i in range(n - horizon_days):
            ret = (closes[i + horizon_days] - closes[i]) / closes[i]
            returns[sid][tss[i]] = ret
    return returns

def compute_prior_return(bars, lookback_days=5):
    rets = defaultdict(dict)
    for sid, series in bars.items():
        closes = [c for _, c in series]
        tss = [t for t, _ in series]
        n = len(closes)
        for i in range(lookback_days, n):
            ret = (closes[i] - closes[i - lookback_days]) / closes[i - lookback_days]
            rets[sid][tss[i]] = ret
    return rets

def compute_daily_return(bars):
    rets = defaultdict(dict)
    for sid, series in bars.items():
        closes = [c for _, c in series]
        tss = [t for t, _ in series]
        for i in range(1, len(closes)):
            ret = (closes[i] - closes[i-1]) / closes[i-1]
            rets[sid][tss[i]] = ret
    return rets

def rolling_median(values, window):
    if len(values) < window:
        return None
    sorted_vals = sorted(values[-window:])
    mid = window // 2
    if window % 2 == 0:
        return (sorted_vals[mid-1] + sorted_vals[mid]) / 2
    return sorted_vals[mid]

def main():
    conn = connect_ro()
    
    symbols = load_symbols(conn)
    stocktwits = load_stocktwits(conn)
    news_sent = load_news_sentiment(conn)
    bars = load_bars_1d(conn)
    shares_data = load_shares_outstanding(conn)
    
    common_sids = set(symbols.keys()) & set(stocktwits.keys()) & set(bars.keys()) & set(news_sent.keys())
    if not common_sids:
        print("INSUFFICIENT=1")
        return
    
    fwd_returns = compute_returns(bars, 5)
    prior_5d_rets = compute_prior_return(bars, 5)
    daily_rets = compute_daily_return(bars)
    
    all_decisions = []
    
    for sid in common_sids:
        tw_series = stocktwits[sid]
        if len(tw_series) < 100:
            continue
        
        bar_series = bars[sid]
        bar_ts_set = set(t for t, _ in bar_series)
        bar_closes = {t: c for t, c in bar_series}
        
        tw_by_date = defaultdict(list)
        for ts, total in tw_series:
            day = ts_to_date(ts)
            tw_by_date[day].append(total)
        
        tw_daily_total = {day: sum(vals) for day, vals in tw_by_date.items()}
        sorted_days = sorted(tw_daily_total.keys())
        
        for i, day in enumerate(sorted_days):
            if i < 20:
                continue
            
            window_vals = [tw_daily_total[sorted_days[j]] for j in range(i-20, i)]
            median_20 = rolling_median(window_vals, 20)
            if median_20 is None or median_20 == 0:
                continue
            
            today_total = tw_daily_total[day]
            if today_total < 50:
                continue
            
            ratio = today_total / median_20
            if ratio <= 3.0:
                continue
            
            if i > 0:
                prev_day = sorted_days[i-1]
                prev_total = tw_daily_total[prev_day]
                prev_ratio = prev_total / median_20 if median_20 else 0
                if prev_ratio > 2.0:
                    continue
            
            day_ts = date_to_ts(day)
            if day_ts not in bar_ts_set:
                continue
            
            news_score = news_sent[sid].get(day, 0.0)
            if abs(news_score) > 0.1:
                continue
            if abs(news_score) > 0.3:
                continue
            
            daily_ret = daily_rets[sid].get(day_ts, 0.0)
            if abs(daily_ret) >= 0.01:
                continue
            
            prior_ret = prior_5d_rets[sid].get(day_ts, 0.0)
            if abs(prior_ret) < 0.005:
                continue
            
            call_direction = 1 if prior_ret > 0 else -1
            
            fwd_ret = fwd_returns[sid].get(day_ts)
            if fwd_ret is None:
                continue
            
            realized_direction = 1 if fwd_ret > 0 else -1
            hit = 1 if realized_direction == call_direction else 0
            
            shares = get_shares_asof(shares_data, sid, day_ts)
            if shares is None:
                continue
            price = bar_closes[day_ts]
            mkt_cap = shares * price
            if mkt_cap < 500_000_000:
                continue
            
            all_decisions.append({
                'symbol_id': sid,
                'day': day,
                'day_ts': day_ts,
                'call_dir': call_direction,
                'hit': hit,
                'fwd_ret': fwd_ret
            })
    
    if not all_decisions:
        print("INSUFFICIENT=1")
        return
    
    all_decisions.sort(key=lambda x: x['day_ts'])
    
    n_total = len(all_decisions)
    split_idx = int(n_total * 0.8)
    unsealed = all_decisions[:split_idx]
    sealed = all_decisions[split_idx:]
    
    def compute_metrics(decisions):
        if not decisions:
            return None
        issued = len(decisions)
        hits = sum(d['hit'] for d in decisions)
        precision = hits / issued if issued else 0.0
        
        pos_calls = sum(1 for d in decisions if d['call_dir'] == 1)
        neg_calls = issued - pos_calls
        pos_hits = sum(1 for d in decisions if d['call_dir'] == 1 and d['hit'] == 1)
        neg_hits = sum(1 for d in decisions if d['call_dir'] == -1 and d['hit'] == 1)
        
        base_rate = max(pos_calls, neg_calls) / issued if issued else 0.0
        
        distinct_days = len(set(d['day'] for d in decisions))
        
        day_counts = defaultdict(int)
        for d in decisions:
            day_counts[d['day']] += 1
        design_effect = sum(c*c for c in day_counts.values()) / issued if issued else 1.0
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }
    
    unsealed_m = compute_metrics(unsealed)
    sealed_m = compute_metrics(sealed)
    
    if unsealed_m is None:
        print("INSUFFICIENT=1")
        return
    
    print(f"ISSUED={unsealed_m['issued']}")
    print(f"OPPORTUNITIES={n_total}")
    print(f"PRECISION={unsealed_m['precision']:.6f}")
    print(f"BASE_RATE={unsealed_m['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={unsealed_m['distinct_days']}")
    print(f"EFFECTIVE_N={unsealed_m['effective_n']:.6f}")
    if sealed_m:
        print(f"SEALED_PRECISION={sealed_m['precision']:.6f}")
    else:
        print("SEALED_PRECISION=0.000000")

if __name__ == '__main__':
    main()