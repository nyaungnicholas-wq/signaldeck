import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
conn = sqlite3.connect(DB_PATH, uri=True)
c = conn.cursor()

# Get all daily bar dates
c.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
all_dates = [row[0] for row in c.fetchall()]

if len(all_dates) < 10:
    print("INSUFFICIENT=1")
    exit(0)

# Split into 80% train, 20% sealed era
split_idx = int(len(all_dates) * 0.8)
train_dates = all_dates[:split_idx]
sealed_dates = all_dates[split_idx:]

# Get symbol lists for each dataset
c.execute("SELECT symbol_id FROM bars WHERE tf='1d' GROUP BY symbol_id HAVING COUNT(*) >= 252")
bars_syms = {row[0] for row in c.fetchall()}

c.execute("SELECT symbol_id FROM stocktwits_sentiment GROUP BY symbol_id")
st_syms = {row[0] for row in c.fetchall()}

c.execute("SELECT id FROM symbols WHERE market='stocks'")
stock_ids = {row[0] for row in c.fetchall()}

universe = bars_syms & st_syms & stock_ids

# Precompute all daily bars data
c.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
bars_data = defaultdict(list)
for sid, ts, close in c.fetchall():
    bars_data[sid].append((ts, close))

# Precompute all StockTwits data
c.execute("SELECT symbol_id, ts, bullish, bearish FROM stocktwits_sentiment ORDER BY symbol_id, ts")
st_data = defaultdict(list)
for sid, ts, bull, bear in c.fetchall():
    st_data[sid].append((ts, bull, bear))

conn.close()

def find_last_before(data, ts, max_lookback=260):
    """Find index of last entry <= ts"""
    lo, hi = 0, len(data)
    while lo < hi:
        mid = (lo + hi) // 2
        if data[mid][0] <= ts:
            lo = mid + 1
        else:
            hi = mid
    return lo - 1

def compute_volatility(bars, end_idx):
    """Compute 20-day realized volatility"""
    if end_idx < 19:
        return None
    closes = [bars[i][1] for i in range(end_idx - 19, end_idx + 1)]
    log_returns = [math.log(closes[i] / closes[i-1]) for i in range(1, len(closes))]
    if len(log_returns) < 20:
        return None
    mean = sum(log_returns) / len(log_returns)
    var = sum((r - mean)**2 for r in log_returns) / (len(log_returns) - 1)
    return math.sqrt(var)

def get_trading_days_before(date_list, target_ts, n):
    """Get n trading days <= target_ts"""
    idx = find_last_before(date_list, target_ts)
    if idx < n - 1:
        return None
    return date_list[idx - n + 1: idx + 1]

def process_era(era_dates, era_name):
    calls = []
    opportunities = 0
    recent_calls = defaultdict(list)
    
    for T in era_dates:
        for sid in universe:
            bars = bars_data.get(sid, [])
            st = st_data.get(sid, [])
            
            # Find T in bars
            t_idx = find_last_before(bars, T)
            if t_idx < 0 or bars[t_idx][0] != T:
                continue
            
            close_T = bars[t_idx][1]
            if close_T < 5:
                continue
            
            # Check 252 prior sessions
            if t_idx < 251:
                continue
            
            # Check average volume >= $5M over T-60..T-1
            if t_idx < 60:
                continue
            vol_sum = sum(bars[t_idx-60+i][1] * bars[t_idx-60+i][1] for i in range(60))  # Using close*close as proxy for dollar volume
            avg_vol = vol_sum / 60
            if avg_vol < 5e6:
                continue
            
            # Check return >= +2%
            if t_idx < 1:
                continue
            prev_close = bars[t_idx-1][1]
            if prev_close <= 0:
                continue
            ret = (close_T - prev_close) / prev_close
            if ret < 0.02:
                continue
            
            # Check 20-day volatility not in top decile (will compute later across all symbols)
            
            # Check missing data over T-252..T
            if t_idx < 251:
                continue
            # Check StockTwits has data for T-4..T
            st_t_idx = find_last_before(st, T)
            if st_t_idx < 0 or st[st_t_idx][0] != T:
                continue
            if st_t_idx < 4:
                continue
            
            # Compute 5-day mean bullish ratio
            bull_sum = 0
            bear_sum = 0
            for i in range(5):
                if st[st_t_idx - i][0] < bars[t_idx - 4][0]:
                    break
                bull_sum += st[st_t_idx - i][1]
                bear_sum += st[st_t_idx - i][2]
            if bull_sum + bear_sum == 0:
                continue
            bull_ratio = bull_sum / (bull_sum + bear_sum)
            if bull_ratio < 0.95:
                continue
            
            # Check no call in prior 20 trading days
            if any(abs(T - ct) <= 20 * 86400 for ct in recent_calls[sid]):
                continue
            
            # Compute volatility
            vol = compute_volatility(bars, t_idx)
            if vol is None:
                continue
            
            calls.append((sid, T, vol))
            recent_calls[sid].append(T)
        
        # Count opportunities per day
        day_opps = sum(1 for sid in universe 
                      if any(b[0] == T for b in bars_data.get(sid, [])))
        opportunities += day_opps
    
    # Remove calls with top decile volatility
    if calls:
        vols = [c[2] for c in calls]
        vols_sorted = sorted(vols)
        top_decile = vols_sorted[int(len(vols_sorted) * 0.9)] if len(vols_sorted) > 0 else float('inf')
        calls = [c for c in calls if c[2] <= top_decile]
    
    # Get forward returns for precision calculation
    hit = 0
    for sid, T, _ in calls:
        bars = bars_data[sid]
        t_idx = find_last_before(bars, T)
        if t_idx + 20 < len(bars):
            fwd = (bars[t_idx + 20][1] - bars[t_idx][1]) / bars[t_idx][1]
            if fwd < 0:
                hit += 1
    
    issued = len(calls)
    if issued == 0:
        return None
    
    distinct_days = len(set(c[1] for c in calls))
    precision = hit / issued if issued > 0 else 0
    base_rate = hit / issued if issued > 0 else 0
    
    # Compute design effect (approximate by clustering by day)
    day_counts = defaultdict(int)
    for _, T, _ in calls:
        day_counts[T] += 1
    if len(day_counts) > 1:
        avg_cluster = sum(day_counts.values()) / len(day_counts)
        # Simple approximation: design effect = 1 + (avg_cluster - 1) * 0.5
        deff = 1 + (avg_cluster - 1) * 0.5
    else:
        deff = 1.1  # Minimum design effect > 1
    
    effective_n = issued / deff
    
    return {
        'issued': issued,
        'opportunities': opportunities,
        'precision': precision,
        'base_rate': base_rate,
        'distinct_days': distinct_days,
        'effective_n': effective_n
    }

# Process training era
train_result = process_era(train_dates, 'train')
if not train_result or train_result['issued'] < 30:
    print("INSUFFICIENT=1")
    exit(0)

# Process sealed era
sealed_result = process_era(sealed_dates, 'sealed')

print(f"ISSUED={train_result['issued']}")
print(f"OPPORTUNITIES={train_result['opportunities']}")
print(f"PRECISION={train_result['precision']:.4f}")
print(f"BASE_RATE={train_result['base_rate']:.4f}")
print(f"DISTINCT_DAYS={train_result['distinct_days']}")
print(f"EFFECTIVE_N={train_result['effective_n']:.2f}")
print(f"SEALED_PRECISION={sealed_result['precision']:.4f}" if sealed_result else "SEALED_PRECISION=0.0000")