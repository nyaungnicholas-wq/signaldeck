# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 427
# cycle_index: 18
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
import math

def get_cursor():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    return conn, conn.cursor()

def get_universe(c):
    c.execute("""
        SELECT b.symbol_id, COUNT(DISTINCT b.ts) as bar_days,
               MIN(b.ts) as first_bar, MAX(b.ts) as last_bar
        FROM bars b
        WHERE b.tf='1d'
        GROUP BY b.symbol_id
        HAVING COUNT(DISTINCT b.ts) >= 5*252
    """)
    bars_qual = {r[0]: (r[1], r[2], r[3]) for r in c.fetchall()}
    
    c.execute("""
        SELECT symbol_id, COUNT(DISTINCT day) as sent_days,
               MIN(day) as first_sent, MAX(day) as last_sent
        FROM sentiment_features
        GROUP BY symbol_id
        HAVING COUNT(DISTINCT day) >= 5*252
    """)
    sent_qual = {r[0]: (r[1], r[2], r[3]) for r in c.fetchall()}
    
    symbols = set(bars_qual.keys()) & set(sent_qual.keys())
    
    vol_5day = {}
    for sym in symbols:
        c.execute("""
            SELECT AVG(close * volume) as avg_dollar_vol
            FROM (
                SELECT close, volume
                FROM bars
                WHERE symbol_id=? AND tf='1d'
                ORDER BY ts DESC
                LIMIT 5
            )
        """, (sym,))
        row = c.fetchone()
        if row and row[0] and row[0] > 10_000_000:
            vol_5day[sym] = row[0]
    
    return set(vol_5day.keys())

def get_outcomes(c):
    outcomes = {}
    c.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon=21
    """)
    for sym_id, ts, up, fwd_return in c.fetchall():
        if sym_id not in outcomes:
            outcomes[sym_id] = {}
        outcomes[sym_id][ts] = (up, fwd_return)
    return outcomes

def get_insider_trades(c):
    c.execute("""
        SELECT symbol_id, tx_ts, filed_ts, code, value
        FROM insider_trades
        WHERE code='P'
    """)
    trades = []
    for row in c.fetchall():
        trades.append({
            'symbol_id': row[0],
            'tx_ts': row[1],
            'filed_ts': row[2],
            'code': row[3],
            'value': row[4]
        })
    return trades

def get_sentiment_stats(c, symbol_id):
    c.execute("""
        SELECT day, mean_score
        FROM sentiment_features
        WHERE symbol_id=?
        ORDER BY day
    """, (symbol_id,))
    data = c.fetchall()
    if not data:
        return None
    scores = [r[1] for r in data]
    sorted_scores = sorted(scores)
    idx_20 = int(len(sorted_scores) * 0.2)
    p20 = sorted_scores[idx_20]
    day_map = {r[0]: r[1] for r in data}
    return p20, day_map

def get_bars_dict(c, symbol_id):
    c.execute("""
        SELECT ts, close
        FROM bars
        WHERE symbol_id=? AND tf='1d'
        ORDER BY ts
    """, (symbol_id,))
    return {r[0]: r[1] for r in c.fetchall()}

def get_float(c, symbol_id, ts):
    c.execute("""
        SELECT value
        FROM fundamentals
        WHERE symbol_id=? AND metric='EntityPublicFloat' AND fetched_at<=?
        ORDER BY fetched_at DESC
        LIMIT 1
    """, (symbol_id, ts))
    row = c.fetchone()
    return row[0] if row else None

def get_recent_f4(c, symbol_id, ts, window=20*86400):
    start = ts - window
    c.execute("""
        SELECT COUNT(*)
        FROM insider_trades
        WHERE symbol_id=? AND filed_ts>=? AND filed_ts<? AND code IN ('P','S')
    """, (symbol_id, start, ts))
    row = c.fetchone()
    return row[0] > 0 if row else False

def date_to_ts(date_str):
    return int(datetime.strptime(date_str, '%Y-%m-%d').timestamp())

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def percentile(values, pct):
    if not values:
        return None
    sorted_vals = sorted(values)
    idx = int(len(sorted_vals) * pct / 100)
    return sorted_vals[min(idx, len(sorted_vals)-1)]

def main():
    conn, c = get_cursor()
    try:
        universe = get_universe(c)
        if len(universe) == 0:
            print("INSUFFICIENT=1")
            return
        
        outcomes = get_outcomes(c)
        trades = get_insider_trades(c)
        
        issued = []
        opportunities = 0
        
        for trade in trades:
            sym = trade['symbol_id']
            if sym not in universe:
                continue
            
            filed_ts = trade['filed_ts']
            if filed_ts is None:
                continue
            
            opportunities += 1
            
            if trade['value'] <= 100000:
                continue
            
            sent_stats = get_sentiment_stats(c, sym)
            if not sent_stats:
                continue
            p20, day_map = sent_stats
            
            bars = get_bars_dict(c, sym)
            if not bars:
                continue
            
            trade_date = ts_to_date(filed_ts)
            trade_ts = date_to_ts(trade_date)
            
            close_dates = sorted([ts_to_date(ts) for ts in bars.keys()])
            trade_idx = None
            for i, d in enumerate(close_dates):
                if d >= trade_date:
                    trade_idx = i
                    break
            if trade_idx is None:
                continue
            
            if trade_idx < 40:
                continue
            
            ret_dates = close_dates[trade_idx-40:trade_idx]
            if len(ret_dates) < 40:
                continue
            
            close_start = bars[date_to_ts(ret_dates[0])]
            close_end = bars[date_to_ts(ret_dates[-1])]
            cum_ret = (close_end / close_start) - 1
            if cum_ret > -0.15:
                continue
            
            sent_dates = [ts_to_date(date_to_ts(d)) for d in close_dates[trade_idx-20:trade_idx]]
            below_count = 0
            for d in sent_dates:
                if d in day_map and day_map[d] < p20:
                    below_count += 1
            if below_count < 10:
                continue
            
            float_val = get_float(c, sym, filed_ts)
            if float_val is not None and float_val < 500_000_000:
                continue
            
            if get_recent_f4(c, sym, filed_ts):
                continue
            
            if sym in outcomes and trade_ts in outcomes[sym]:
                up, fwd_return = outcomes[sym][trade_ts]
            else:
                continue
            
            issued.append({
                'symbol_id': sym,
                'filed_ts': filed_ts,
                'date': trade_date,
                'up': up,
                'fwd_return': fwd_return
            })
        
        if not issued:
            print("INSUFFICIENT=1")
            return
        
        issued.sort(key=lambda x: x['filed_ts'])
        
        n_issued = len(issued)
        n_sealed = max(1, int(n_issued * 0.2))
        sealed = issued[-n_sealed:]
        train = issued[:-n_sealed]
        
        hits_train = sum(1 for x in train if x['up'])
        precision_train = hits_train / len(train) if train else 0
        
        hits_sealed = sum(1 for x in sealed if x['up'])
        precision_sealed = hits_sealed / len(sealed) if sealed else 0
        
        base_rate_train = precision_train
        
        distinct_days_train = len(set(x['date'] for x in train))
        n_clusters = distinct_days_train
        cluster_sizes = []
        day_counts = {}
        for x in train:
            day_counts[x['date']] = day_counts.get(x['date'], 0) + 1
        cluster_sizes = list(day_counts.values())
        avg_cluster = sum(cluster_sizes) / n_clusters if n_clusters else 1
        rho = 0.5
        design_effect = 1 + (avg_cluster - 1) * rho
        effective_n = n_issued / design_effect if design_effect > 0 else n_issued
        
        print(f"ISSUED={n_issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision_train:.4f}")
        print(f"BASE_RATE={base_rate_train:.4f}")
        print(f"DISTINCT_DAYS={distinct_days_train}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={precision_sealed:.4f}")
        
    finally:
        conn.close()

if __name__ == "__main__":
    main()