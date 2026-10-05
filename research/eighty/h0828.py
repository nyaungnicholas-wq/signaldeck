# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 827
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict, deque
from bisect import insort, bisect_left
from datetime import datetime, timezone

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def median_of_sorted(lst):
    n = len(lst)
    if n == 0:
        return None
    if n % 2 == 1:
        return lst[n // 2]
    return (lst[n // 2 - 1] + lst[n // 2]) / 2.0

def percentile_of_sorted(lst, p):
    if not lst:
        return None
    k = (len(lst) - 1) * p
    f = int(k)
    c = min(f + 1, len(lst) - 1)
    if f == c:
        return lst[f]
    return lst[f] + (lst[c] - lst[f]) * (k - f)

def is_officer_title(title):
    if not title:
        return False
    t = title.lower()
    return ('chief executive' in t or 'ceo' in t or 
            'chief financial' in t or 'cfo' in t)

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get active stock symbols
    cur.execute("SELECT id, symbol FROM symbols WHERE active=1 AND market='stocks'")
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    # Get qualifying insider trades: code='P' (purchase), officer titles
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, value, title
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({})
    """.format(','.join('?'*len(symbols))), list(symbols.keys()))
    
    insider_by_sym = defaultdict(list)
    for row in cur.fetchall():
        if is_officer_title(row['title']):
            insider_by_sym[row['symbol_id']].append({
                'tx_ts': row['tx_ts'],
                'filed_ts': row['filed_ts'],
                'value': row['value'],
                'title': row['title']
            })

    # Get daily bars (tf='1d') for all symbols
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbols))), list(symbols.keys()))
    
    bars_by_sym = defaultdict(list)
    for row in cur.fetchall():
        bars_by_sym[row['symbol_id']].append({
            'ts': row['ts'],
            'date': epoch_to_date(row['ts']),
            'close': row['close'],
            'volume': row['volume']
        })

    # Get sentiment_features (daily mean_score)
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, day
    """.format(','.join('?'*len(symbols))), list(symbols.keys()))
    
    sent_by_sym = defaultdict(dict)
    for row in cur.fetchall():
        sent_by_sym[row['symbol_id']][row['day']] = row['mean_score']

    observations = []  # (filed_date, symbol_id, fwd_return, hit)

    for sym_id, sym_bars in bars_by_sym.items():
        if len(sym_bars) < 252 + 21:
            continue
        if sym_id not in insider_by_sym or not insider_by_sym[sym_id]:
            continue
        if sym_id not in sent_by_sym or not sent_by_sym[sym_id]:
            continue

        # Build aligned daily series
        bar_by_date = {b['date']: b for b in sym_bars}
        sent_data = sent_by_sym[sym_id]
        trades = insider_by_sym[sym_id]

        # Sort dates
        all_dates = sorted(bar_by_date.keys())
        date_to_idx = {d: i for i, d in enumerate(all_dates)}

        # Precompute rolling 252-day min close, 20-day median volume, 252-day sentiment decile
        n = len(all_dates)
        min_close_252 = [None] * n
        med_vol_20 = [None] * n
        sent_decile_252 = [None] * n  # bottom decile threshold (10th percentile)

        close_deque = deque(maxlen=252)
        vol_deque = deque(maxlen=20)
        sent_deque = deque(maxlen=252)
        sent_sorted = []

        for i, d in enumerate(all_dates):
            b = bar_by_date[d]
            close_deque.append(b['close'])
            vol_deque.append(b['volume'])
            
            if d in sent_data:
                s = sent_data[d]
                sent_deque.append(s)
                insort(sent_sorted, s)
                if len(sent_deque) > 252:
                    old = sent_deque[0]  # actually deque[0] is oldest, but we need to remove the one falling out
                    # Better: track what falls out
                    pass

            # This approach is flawed for sliding window removal from sorted list
            # Let's recompute per symbol more carefully

        # Recompute properly with sliding window
        close_vals = [bar_by_date[d]['close'] for d in all_dates]
        vol_vals = [bar_by_date[d]['volume'] for d in all_dates]
        sent_vals = [sent_data.get(d) for d in all_dates]

        for i in range(n):
            # 252-day min close (including current day)
            start = max(0, i - 251)
            min_close_252[i] = min(close_vals[start:i+1])
            
            # 20-day median volume (prior 20 days, not including current)
            vol_start = max(0, i - 20)
            vol_end = i
            if vol_end > vol_start:
                med_vol_20[i] = median_of_sorted(sorted(vol_vals[vol_start:vol_end]))
            
            # 252-day sentiment 10th percentile (prior 252 days, not including current)
            sent_start = max(0, i - 252)
            sent_end = i
            prior_sent = [v for v in sent_vals[sent_start:sent_end] if v is not None]
            if len(prior_sent) >= 50:  # need sufficient data for decile
                sent_decile_252[i] = percentile_of_sorted(sorted(prior_sent), 0.10)

        # Check each insider trade
        for tr in trades:
            tx_date = epoch_to_date(tr['tx_ts'])
            filed_date = epoch_to_date(tr['filed_ts'])
            
            if tx_date not in date_to_idx or filed_date not in date_to_idx:
                continue
            
            tx_idx = date_to_idx[tx_date]
            filed_idx = date_to_idx[filed_date]
            
            # Must have 21 days forward from filed_date
            if filed_idx + 21 >= n:
                continue
            
            # Conditions at tx_date (trade date)
            b_tx = bar_by_date[tx_date]
            
            # 1. New 252-day low (close == 252-day min)
            if min_close_252[tx_idx] is None or b_tx['close'] != min_close_252[tx_idx]:
                continue
            
            # 2. Volume > 1.5x 20-day median volume (prior 20 days)
            if med_vol_20[tx_idx] is None or med_vol_20[tx_idx] == 0:
                continue
            if b_tx['volume'] <= 1.5 * med_vol_20[tx_idx]:
                continue
            
            # 3. Sentiment at tx_date in bottom decile of prior 252 days
            sent_tx = sent_data.get(date_to_str(tx_date))
            if sent_tx is None or sent_decile_252[tx_idx] is None:
                continue
            if sent_tx > sent_decile_252[tx_idx]:
                continue
            
            # Entry at first bar after filed_date (filed_idx + 1)
            entry_idx = filed_idx + 1
            if entry_idx + 21 > n:
                continue
            
            entry_close = close_vals[entry_idx]
            exit_close = close_vals[entry_idx + 21]
            fwd_return = (exit_close - entry_close) / entry_close
            hit = 1 if fwd_return > 0 else 0
            
            observations.append((filed_date, sym_id, fwd_return, hit))

    if not observations:
        print("INSUFFICIENT=1")
        return 0

    # Sort by filed_date
    observations.sort(key=lambda x: x[0])
    
    # Hold out most recent 20% of distinct decision days as sealed era
    decision_days = sorted(set(obs[0] for obs in observations))
    n_days = len(decision_days)
    if n_days < 5:
        print("INSUFFICIENT=1")
        return 0
    
    split_idx = int(n_days * 0.8)
    sealed_days = set(decision_days[split_idx:])
    
    # Split observations
    train_obs = [o for o in observations if o[0] not in sealed_days]
    sealed_obs = [o for o in observations if o[0] in sealed_days]
    
    def compute_metrics(obs_list):
        if not obs_list:
            return None
        issued = len(obs_list)
        hits = sum(o[3] for o in obs_list)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # base rate within issued subset
        distinct_days = len(set(o[0] for o in obs_list))
        
        # Design effect: 1 + (avg_cluster_size - 1) * ICC
        # Approximate: group by day, compute variance of daily hit rates
        day_hits = defaultdict(lambda: [0, 0])  # day -> [hits, count]
        for o in obs_list:
            day_hits[o[0]][0] += o[3]
            day_hits[o[0]][1] += 1
        
        daily_rates = [h/c for h, c in day_hits.values() if c > 0]
        if len(daily_rates) > 1:
            mean_rate = sum(daily_rates) / len(daily_rates)
            var_rate = sum((r - mean_rate)**2 for r in daily_rates) / (len(daily_rates) - 1)
            avg_cluster = issued / len(daily_rates)
            icc = var_rate / (mean_rate * (1 - mean_rate) + 1e-12) if mean_rate > 0 and mean_rate < 1 else 0
            deff = 1 + (avg_cluster - 1) * max(0, icc)
        else:
            deff = 1.0
        
        effective_n = issued / deff if deff > 0 else issued
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n,
            'deff': deff
        }
    
    train_m = compute_metrics(train_obs)
    sealed_m = compute_metrics(sealed_obs)
    
    if not train_m or not sealed_m:
        print("INSUFFICIENT=1")
        return 0
    
    # Opportunities: count of decision points considered (officer trades meeting basic criteria)
    # For simplicity, use total officer trades as opportunities
    cur.execute("""
        SELECT COUNT(*) FROM insider_trades
        WHERE code='P' AND symbol_id IN ({})
    """.format(','.join('?'*len(symbols))), list(symbols.keys()))
    opportunities = cur.fetchone()[0]
    
    print(f"ISSUED={train_m['issued']}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_m['precision']:.6f}")
    print(f"BASE_RATE={train_m['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_m['distinct_days']}")
    print(f"EFFECTIVE_N={train_m['effective_n']:.2f}")
    print(f"SEALED_PRECISION={sealed_m['precision']:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())