import sqlite3
import sys
from datetime import datetime, timezone, date
from collections import defaultdict
import math
import statistics

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def get_eligible_symbols(conn):
    cur = conn.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.active = 1 AND s.market = 'stocks'
        AND EXISTS (SELECT 1 FROM bars b WHERE b.symbol_id = s.id AND b.tf = '1d')
        AND EXISTS (SELECT 1 FROM sentiment_features sf WHERE sf.symbol_id = s.id)
    """)
    return [dict(row) for row in cur.fetchall()]

def load_bars(conn, symbol_id):
    cur = conn.execute("""
        SELECT ts, open, high, low, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (symbol_id,))
    rows = cur.fetchall()
    return [(ts_to_date(r['ts']), r['open'], r['high'], r['low'], r['close'], r['volume']) for r in rows]

def load_sentiment(conn, symbol_id):
    cur = conn.execute("""
        SELECT day, mean_score
        FROM sentiment_features
        WHERE symbol_id = ?
        ORDER BY day
    """, (symbol_id,))
    rows = cur.fetchall()
    return {datetime.strptime(r['day'], '%Y-%m-%d').date(): r['mean_score'] for r in rows}

def compute_rolling(bars, sentiment):
    n = len(bars)
    if n < 252 + 20:
        return []
    
    dates = [b[0] for b in bars]
    closes = [b[4] for b in bars]
    volumes = [b[5] for b in bars]
    
    # Precompute daily returns
    returns = [0.0] * n
    for i in range(1, n):
        if closes[i-1] > 0:
            returns[i] = (closes[i] - closes[i-1]) / closes[i-1]
    
    results = []
    for i in range(252, n - 20):
        d = dates[i]
        close = closes[i]
        vol = volumes[i]
        
        # Universe filters
        if close < 5:
            continue
        
        # 60-session avg dollar volume
        dollar_vols = [closes[j] * volumes[j] for j in range(i-59, i+1)]
        avg_dollar_vol = sum(dollar_vols) / 60
        if avg_dollar_vol < 5_000_000:
            continue
        
        # Sentiment for T-4..T (5 days)
        sent_vals = []
        missing_sent = False
        for k in range(5):
            sd = dates[i - k]
            if sd in sentiment:
                sent_vals.append(sentiment[sd])
            else:
                missing_sent = True
                break
        if missing_sent or len(sent_vals) < 5:
            continue
        sent_5d = sum(sent_vals) / 5
        
        # 1-day return
        ret_1d = returns[i]
        
        # SMA 200
        sma_200 = sum(closes[i-199:i+1]) / 200
        
        # 20-session median volume
        vol_20 = volumes[i-19:i+1]
        median_vol_20 = statistics.median(vol_20)
        
        # 20-session realized volatility (std of daily returns)
        ret_20 = returns[i-19:i+1]
        if len(ret_20) >= 2:
            vol_20d = statistics.stdev(ret_20) * math.sqrt(252)
        else:
            vol_20d = 0
        
        # Forward 20-day return
        fwd_close = closes[i + 20]
        fwd_return = (fwd_close - close) / close if close > 0 else 0
        
        results.append({
            'symbol_id': None,  # filled later
            'date': d,
            'close': close,
            'sentiment_5d': sent_5d,
            'ret_1d': ret_1d,
            'sma_200': sma_200,
            'volume': vol,
            'median_vol_20': median_vol_20,
            'vol_20d': vol_20d,
            'fwd_return_20d': fwd_return,
        })
    return results

def percentile(sorted_vals, p):
    if not sorted_vals:
        return None
    k = (len(sorted_vals) - 1) * p / 100
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return sorted_vals[int(k)]
    return sorted_vals[f] + (sorted_vals[c] - sorted_vals[f]) * (k - f)

def compute_design_effect(calls):
    if len(calls) < 2:
        return 1.0
    
    monthly = defaultdict(list)
    for c in calls:
        key = (c['date'].year, c['date'].month)
        monthly[key].append(c['hit'])
    
    cluster_sizes = [len(v) for v in monthly.values()]
    k = len(cluster_sizes)
    n = sum(cluster_sizes)
    
    if k <= 1:
        return 1.0
    
    mean_size = n / k
    p_overall = sum(1 for c in calls if c['hit']) / n
    
    if p_overall == 0 or p_overall == 1:
        return 1.0
    
    # Between and within sum of squares for binary data
    bss = 0.0
    wss = 0.0
    for hits in monthly.values():
        m = len(hits)
        if m == 0:
            continue
        p_cluster = sum(hits) / m
        bss += m * (p_cluster - p_overall) ** 2
        wss += m * p_cluster * (1 - p_cluster)
    
    if wss == 0:
        return 1.0
    
    msb = bss / (k - 1)
    msw = wss / (n - k)
    
    if msb <= msw:
        return 1.0
    
    icc = (msb - msw) / (msb + (mean_size - 1) * msw)
    icc = max(0.0, min(1.0, icc))
    deff = 1 + (mean_size - 1) * icc
    return max(1.0, deff)

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    symbols = get_eligible_symbols(conn)
    if len(symbols) < 30:
        print("INSUFFICIENT=1")
        return
    
    all_points = []
    for sym in symbols:
        bars = load_bars(conn, sym['id'])
        sent = load_sentiment(conn, sym['id'])
        pts = compute_rolling(bars, sent)
        for p in pts:
            p['symbol_id'] = sym['id']
        all_points.extend(pts)
    
    if not all_points:
        print("INSUFFICIENT=1")
        return
    
    # Group by date
    by_date = defaultdict(list)
    for p in all_points:
        by_date[p['date']].append(p)
    
    dates_sorted = sorted(by_date.keys())
    if len(dates_sorted) < 10:
        print("INSUFFICIENT=1")
        return
    
    # Determine sealed era cutoff (most recent 20% of dates)
    split_idx = int(len(dates_sorted) * 0.8)
    sealed_cutoff = dates_sorted[split_idx]
    
    calls = []
    opportunities = 0
    last_call = {}  # symbol_id -> last call date
    
    for d in dates_sorted:
        candidates = by_date[d]
        opportunities += len(candidates)
        
        # Cross-sectional deciles
        sent_vals = sorted([c['sentiment_5d'] for c in candidates if c['sentiment_5d'] is not None])
        vol_vals = sorted([c['vol_20d'] for c in candidates if c['vol_20d'] is not None])
        
        if len(sent_vals) < 10 or len(vol_vals) < 10:
            continue
        
        sent_thresh = percentile(sent_vals, 10)
        vol_thresh = percentile(vol_vals, 90)
        
        # Count eligible at T (universe filters only)
        eligible_at_t = sum(1 for c in candidates 
                           if c['sentiment_5d'] is not None and c['vol_20d'] is not None)
        if eligible_at_t < 30:
            continue
        
        for c in candidates:
            if c['sentiment_5d'] is None or c['vol_20d'] is None:
                continue
            if c['sentiment_5d'] > sent_thresh:
                continue
            if c['vol_20d'] > vol_thresh:
                continue
            if c['ret_1d'] < -0.01 or c['ret_1d'] > 0.01:
                continue
            if c['close'] < 5:
                continue
            if c['close'] <= c['sma_200']:
                continue
            if c['volume'] > 1.5 * c['median_vol_20']:
                continue
            if c['symbol_id'] in last_call:
                days_diff = (c['date'] - last_call[c['symbol_id']]).days
                if days_diff < 20:
                    continue
            
            hit = c['fwd_return_20d'] < 0
            calls.append({
                'symbol_id': c['symbol_id'],
                'date': c['date'],
                'fwd_return': c['fwd_return_20d'],
                'hit': hit,
            })
            last_call[c['symbol_id']] = c['date']
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    calls.sort(key=lambda x: x['date'])
    
    # Split by sealed era
    main_calls = [c for c in calls if c['date'] < sealed_cutoff]
    sealed_calls = [c for c in calls if c['date'] >= sealed_cutoff]
    
    if not main_calls:
        print("INSUFFICIENT=1")
        return
    
    issued = len(main_calls)
    hits = sum(1 for c in main_calls if c['hit'])
    precision = hits / issued
    base_rate = sum(1 for c in main_calls if c['fwd_return'] < 0) / issued
    distinct_days = len(set(c['date'] for c in main_calls))
    deff = compute_design_effect(main_calls)
    effective_n = issued / deff
    sealed_precision = sum(1 for c in sealed_calls if c['hit']) / len(sealed_calls) if sealed_calls else 0.0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()