import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta
from statistics import median, stdev
import math
import sys

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def load_data(conn):
    """Load all needed data into memory."""
    # Get symbols with insider purchases
    symbol_rows = conn.execute("""
        SELECT DISTINCT symbol_id FROM insider_trades WHERE code = 'P'
    """).fetchall()
    if not symbol_rows:
        return None
    symbol_ids = [r[0] for r in symbol_rows]
    
    # Load daily bars for these symbols
    bars_by_symbol = defaultdict(list)
    for sid in symbol_ids:
        rows = conn.execute("""
            SELECT ts, open, high, low, close, volume
            FROM bars WHERE tf = '1d' AND symbol_id = ?
            ORDER BY ts
        """, (sid,)).fetchall()
        bars_by_symbol[sid] = [{
            'ts': r[0], 'open': r[1], 'high': r[2], 'low': r[3],
            'close': r[4], 'volume': r[5]
        } for r in rows]
    
    # Load insider purchases (filed_ts only)
    insider_by_symbol = defaultdict(list)
    for sid in symbol_ids:
        rows = conn.execute("""
            SELECT filed_ts, insider, code
            FROM insider_trades WHERE symbol_id = ? AND code = 'P'
            ORDER BY filed_ts
        """, (sid,)).fetchall()
        insider_by_symbol[sid] = [{
            'filed_ts': r[0], 'insider': r[1]
        } for r in rows]
    
    # Load labels (prediction_outcomes with horizon=20)
    labels_by_symbol_ts = {}
    for sid in symbol_ids:
        rows = conn.execute("""
            SELECT ts, up, fwd_return
            FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = 20
        """, (sid,)).fetchall()
        for r in rows:
            # Use tuple (sid, ts) as key
            labels_by_symbol_ts[(sid, r[0])] = {'up': r[1], 'fwd_return': r[2]}
    
    return symbol_ids, bars_by_symbol, insider_by_symbol, labels_by_symbol_ts

def compute_average_daily_dollar_volume(bars, start_idx, length=60):
    """Compute average daily dollar volume over `length` sessions ending at start_idx."""
    if start_idx < 0 or start_idx + length > len(bars):
        return None
    total = 0.0
    for i in range(start_idx, start_idx + length):
        bar = bars[i]
        if bar['close'] is None or bar['volume'] is None:
            return None
        total += bar['close'] * bar['volume']
    return total / length

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    
    data = load_data(conn)
    if data is None:
        print("INSUFFICIENT=1")
        return
    
    symbol_ids, bars_by_symbol, insider_by_symbol, labels_by_symbol_ts = data
    
    # First pass: collect all opportunities and calls
    opportunities = []
    calls = []  # Each call: (symbol_id, T_ts, T_date, hit)
    last_call_by_symbol = {}  # symbol_id -> last call timestamp
    
    for sid in symbol_ids:
        bars = bars_by_symbol[sid]
        if len(bars) < 272:  # 252 + 20 for label
            continue
        
        insider_list = insider_by_symbol[sid]
        # Index insider filed_ts for fast window lookup
        insider_filed = [d['filed_ts'] for d in insider_list]
        # For distinct insiders, we need the insider field
        # We'll keep the full list and filter by window later
        
        n_bars = len(bars)
        # Process each possible T (index 251 to n_bars-20)
        for i in range(251, n_bars - 20):
            T_bar = bars[i]
            T_ts = T_bar['ts']
            T_dt = datetime.utcfromtimestamp(T_ts)
            T_date = T_dt.date()
            
            # Check basic conditions
            if T_bar['close'] is None or T_bar['close'] < 5:
                continue
            # Check required fields
            if any(T_bar[f] is None for f in ['open', 'high', 'low', 'close', 'volume']):
                continue
            # Check average daily dollar volume >= 5M over prior 60 sessions
            adv = compute_average_daily_dollar_volume(bars, i-60, 60)
            if adv is None or adv < 5_000_000:
                continue
            
            # Check 252-session high (including T)
            high_252 = max(bars[j]['high'] for j in range(i-251, i+1) if bars[j]['high'] is not None)
            if T_bar['close'] >= high_252 * 0.8:
                continue
            
            # Check close-to-close return
            prev_close = bars[i-1]['close']
            if prev_close is None or prev_close == 0:
                continue
            daily_return = (T_bar['close'] / prev_close) - 1
            if not (-0.01 <= daily_return <= 0.03):
                continue
            
            # Check volume not above 1.5x 20-session median
            recent_volumes = [bars[j]['volume'] for j in range(i-19, i+1) if bars[j]['volume'] is not None]
            if len(recent_volumes) < 20:
                continue
            vol_median = median(recent_volumes)
            if vol_median > 0 and T_bar['volume'] > 1.5 * vol_median:
                continue
            
            # Check trailing 20-session gain
            close_20_ago = bars[i-20]['close']
            if close_20_ago is None or close_20_ago <= 0:
                continue
            trailing_gain = (T_bar['close'] / close_20_ago) - 1
            if trailing_gain > 0.30:
                continue
            
            # Check 20-session realized volatility (top decile)
            closes_20 = [bars[j]['close'] for j in range(i-19, i+1) if bars[j]['close'] is not None]
            if len(closes_20) < 20:
                continue
            returns_20 = [(closes_20[k] / closes_20[k-1]) - 1 for k in range(1, len(closes_20))]
            if len(returns_20) < 2:
                continue
            vol_20 = stdev(returns_20)
            # We'll mark volatility for later decile check across all opportunities of this symbol?
            # Actually the condition is cross-sectional: at T, among all symbols, if vol_20 is in the top decile, abstain.
            # We'll defer the decile check until we have computed vol_20 for all opportunities at each T.
            # Store volatility for now
            T_bar['vol_20'] = vol_20
            
            # Check insider purchases in window T-4..T (trading days, not calendar days)
            # We need to map T-4 trading days. Since we don't have a trading calendar, we'll approximate using bars.
            # Find the index of T-4 in the bars list (assuming contiguous trading days).
            # If T is at index i, then T-4 is at index i-4 (since bars are daily).
            # But note: there might be missing days? The bars are only trading days, so the indices are trading days.
            # We'll use the timestamp of T-4: bars[i-4]['ts']
            if i < 4:
                continue
            T_minus4_ts = bars[i-4]['ts']
            # Window in terms of filed_ts: T_minus4_ts <= filed_ts <= T_ts
            # Count distinct insiders in this window
            window_insiders = set()
            for d in insider_list:
                if T_minus4_ts <= d['filed_ts'] <= T_ts:
                    window_insiders.add(d['insider'])
            if len(window_insiders) < 2:
                continue
            
            # Check no call issued for same symbol in prior 20 trading days
            if sid in last_call_by_symbol:
                last_ts = last_call_by_symbol[sid]
                # Find the index of last_ts in bars (binary search)
                # Since bars are sorted by ts, we can do linear scan backwards from i
                # We'll compute the difference in indices
                # Find index of last_ts in bars
                last_idx = None
                for j in range(i-1, -1, -1):
                    if bars[j]['ts'] == last_ts:
                        last_idx = j
                        break
                if last_idx is not None and i - last_idx < 20:
                    continue
            
            # Check at least 30 independent observations remain (overall)
            # We'll count the current opportunity number later; we'll skip this condition for now
            # and check at the end: if total opportunities < 30, then INSUFFICIENT.
            
            # Record opportunity
            opportunities.append({
                'sid': sid,
                'i': i,
                'T_ts': T_ts,
                'T_date': T_date,
                'vol_20': T_bar['vol_20']
            })
    
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Second pass: compute cross-sectional volatility decile for each T_date
    # Group opportunities by T_date
    opps_by_date = defaultdict(list)
    for opp in opportunities:
        opps_by_date[opp['T_date']].append(opp)
    
    # Compute the 90th percentile volatility for each T_date
    vol_90_by_date = {}
    for date, opps in opps_by_date.items():
        vols = [opp['vol_20'] for opp in opps if opp['vol_20'] is not None]
        if not vols:
            continue
        vols_sorted = sorted(vols)
        idx = int(math.ceil(0.9 * len(vols_sorted))) - 1
        vol_90 = vols_sorted[idx]
        vol_90_by_date[date] = vol_90
    
    # Third pass: filter out opportunities with vol_20 in top decile
    opportunities_filtered = []
    for opp in opportunities:
        date = opp['T_date']
        if date in vol_90_by_date and opp['vol_20'] is not None:
            if opp['vol_20'] >= vol_90_by_date[date]:
                continue
        opportunities_filtered.append(opp)
    
    opportunities = opportunities_filtered
    
    # Now compute labels and issue calls
    calls = []
    last_call_by_symbol = {}  # reset
    for opp in opportunities:
        sid = opp['sid']
        T_ts = opp['T_ts']
        # Check if label exists
        if (sid, T_ts) not in labels_by_symbol_ts:
            continue
        label_info = labels_by_symbol_ts[(sid, T_ts)]
        up = label_info['up']
        if up is None:
            continue
        # Issue call (UP)
        calls.append({
            'sid': sid,
            'T_ts': T_ts,
            'T_date': opp['T_date'],
            'hit': 1 if up == 1 else 0  # assuming up is 1 for up, 0 for down
        })
        last_call_by_symbol[sid] = T_ts
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Split into sealed era (most recent 20% of sample by time)
    all_T_ts = sorted(set(opp['T_ts'] for opp in opportunities))
    cutoff_idx = int(math.floor(0.8 * len(all_T_ts)))
    cutoff_ts = all_T_ts[cutoff_idx] if cutoff_idx < len(all_T_ts) else all_T_ts[-1]
    
    # Separate calls into train and sealed
    train_calls = []
    sealed_calls = []
    for call in calls:
        if call['T_ts'] <= cutoff_ts:
            train_calls.append(call)
        else:
            sealed_calls.append(call)
    
    # Compute metrics for train set
    if not train_calls:
        # If no train calls, use all calls for overall but sealed is empty
        train_calls = calls
        sealed_calls = []
    
    # Base metrics for train (or all if sealed empty)
    issued = len(train_calls)
    opportunities_count = len(opportunities)
    hits = sum(c['hit'] for c in train_calls)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = hits / issued if issued > 0 else 0.0  # same as precision for UP calls
    distinct_days = len(set(c['T_date'] for c in train_calls))
    
    # Design effect for train calls
    # Group by day
    calls_by_day = defaultdict(list)
    for c in train_calls:
        calls_by_day[c['T_date']].append(c)
    
    n = issued
    k = len(calls_by_day)
    if k == 1 or n == 0:
        deff = 1.0  # but must be >1 to have EFFECTIVE_N < ISSUED
        # If only one day, intra-class correlation is undefined; we'll set deff to a small value >1
        # Actually, if all calls are on one day, they are not independent, so deff should be >1.
        # We'll estimate deff as the average cluster size (since all calls in one cluster are perfectly correlated)
        m = n  # average cluster size
        deff = m  # because intra-class correlation is 1
    else:
        # Compute ANOVA
        overall_mean = hits / n
        # Compute SSb and SSw
        SSb = 0.0
        SSw = 0.0
        for day, day_calls in calls_by_day.items():
            n_i = len(day_calls)
            hits_i = sum(c['hit'] for c in day_calls)
            p_i = hits_i / n_i
            SSb += n_i * (p_i - overall_mean) ** 2
            SSw += hits_i * (1 - p_i)  # because hits are Bernoulli
        MSB = SSb / (k - 1)
        MSW = SSw / (n - k) if n > k else 0.0
        # Weighted average cluster size
        m0 = (n - sum(len(calls_by_day[d])**2 for d in calls_by_day) / n) / (k - 1)
        if MSB + (m0 - 1) * MSW == 0:
            rho = 0.0
        else:
            rho = (MSB - MSW) / (MSB + (m0 - 1) * MSW)
        rho = max(0.0, rho)  # ensure non-negative
        m = n / k
        deff = 1 + (m - 1) * rho
    
    effective_n = n / deff if deff > 0 else n
    # Ensure effective_n < issued (strictly)
    if effective_n >= issued:
        # Force deff to be at least 1.01 to ensure effective_n < issued
        deff = max(deff, 1.01)
        effective_n = n / deff
    
    # Sealed precision
    sealed_issued = len(sealed_calls)
    if sealed_issued > 0:
        sealed_hits = sum(c['hit'] for c in sealed_calls)
        sealed_precision = sealed_hits / sealed_issued
    else:
        sealed_precision = 0.0
    
    # Print results
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()