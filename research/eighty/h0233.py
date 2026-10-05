import sqlite3
import math
from collections import defaultdict

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
conn.row_factory = sqlite3.Row

def get_trading_days(symbol_id, limit=None, before=None):
    query = "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d'"
    params = [symbol_id]
    if before is not None:
        query += " AND ts<?"
        params.append(before)
    query += " ORDER BY ts DESC"
    if limit:
        query += f" LIMIT {limit}"
    return [row['ts'] for row in conn.execute(query, params)]

def get_bar(symbol_id, ts):
    return conn.execute("SELECT * FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (symbol_id, ts)).fetchone()

def get_next_trading_day(symbol_id, from_ts):
    return conn.execute("SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts LIMIT 1", 
                       (symbol_id, from_ts)).fetchone()

def get_60_day_trades(symbol_id, before_ts):
    return conn.execute("""
        SELECT filed_ts FROM insider_trades 
        WHERE symbol_id=? AND code='P' AND filed_ts<? 
        ORDER BY filed_ts DESC
    """, (symbol_id, before_ts)).fetchall()

def get_20_day_volatility(symbol_id, end_ts):
    days = get_trading_days(symbol_id, limit=21, before=end_ts+1)
    if len(days) < 21:
        return None
    closes = []
    for ts in days[:21]:
        bar = get_bar(symbol_id, ts)
        if bar:
            closes.append(bar['close'])
    if len(closes) < 21:
        return None
    returns = [math.log(closes[i]/closes[i+1]) for i in range(len(closes)-1)]
    mean = sum(returns)/len(returns)
    variance = sum((r-mean)**2 for r in returns)/(len(returns)-1)
    return math.sqrt(variance)

def get_decile_threshold(volatilities):
    if not volatilities:
        return None
    sorted_vols = sorted(volatilities.values())
    idx = int(len(sorted_vols) * 0.9)
    return sorted_vols[idx]

def check_conditions(symbol_id, t_ts, event_ts):
    # Get T's bar
    bar_t = get_bar(symbol_id, t_ts)
    if not bar_t:
        return False
    
    # Close >= $5
    if bar_t['close'] < 5:
        return False
    
    # At least 252 prior sessions
    prior_count = conn.execute("SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d' AND ts<?", 
                             (symbol_id, t_ts)).fetchone()[0]
    if prior_count < 252:
        return False
    
    # Average daily dollar volume >= $5M over T-60..T-1
    days_60 = get_trading_days(symbol_id, limit=61, before=t_ts)
    if len(days_60) < 61:
        return False
    dollar_volumes = []
    for ts in days_60[1:]:  # Skip T, get T-60..T-1
        bar = get_bar(symbol_id, ts)
        if bar:
            dollar_volumes.append(bar['close'] * bar['volume'])
    if not dollar_volumes:
        return False
    avg_dollar_vol = sum(dollar_volumes) / len(dollar_volumes)
    if avg_dollar_vol < 5_000_000:
        return False
    
    # T's close-to-close return between -0.5% and +0.5%
    prev_day = get_trading_days(symbol_id, limit=1, before=t_ts)
    if not prev_day:
        return False
    prev_bar = get_bar(symbol_id, prev_day[0])
    if not prev_bar:
        return False
    t_return = (bar_t['close'] - prev_bar['close']) / prev_bar['close']
    if not (-0.005 <= t_return <= 0.005):
        return False
    
    # 20-day realized volatility not in top cross-sectional decile
    vol_20 = get_20_day_volatility(symbol_id, t_ts)
    if vol_20 is None:
        return False
    
    # Check if T+20 exists
    next_20_days = get_trading_days(symbol_id, limit=21, before=t_ts+100*86400)
    if len(next_20_days) < 21:
        return False
    t_plus_20 = next_20_days[20]
    
    return True, t_plus_20, vol_20

# Collect all insider purchases (open-market) with value >= $25k
events = conn.execute("""
    SELECT symbol_id, filed_ts, shares, price, shares * price as value
    FROM insider_trades 
    WHERE code='P' AND shares * price >= 25000
""").fetchall()

opportunities = []
all_volatilities = defaultdict(dict)  # t_ts -> {symbol_id: vol}

for event in events:
    symbol_id = event['symbol_id']
    filed_ts = event['filed_ts']
    
    # Get first trading day on or after disclosure
    next_day = get_next_trading_day(symbol_id, filed_ts)
    if not next_day:
        continue
    t_ts = next_day['ts']
    
    # Check no prior purchase in last 60 trading days
    prior_trades = get_60_day_trades(symbol_id, filed_ts)
    if len(prior_trades) > 0:
        continue
    
    # Check all conditions
    result = check_conditions(symbol_id, t_ts, filed_ts)
    if result is not True:
        continue
    t_plus_20, vol_20 = result[1], result[2]
    
    # Store volatility for decile calculation
    all_volatilities[t_ts][symbol_id] = vol_20
    
    opportunities.append({
        'symbol_id': symbol_id,
        't_ts': t_ts,
        't_plus_20': t_plus_20,
        'filed_ts': filed_ts
    })

if len(opportunities) < 30:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Calculate decile thresholds per day and filter
issued = []
for opp in opportunities:
    t_ts = opp['t_ts']
    if t_ts not in all_volatilities:
        continue
    
    vol_20 = all_volatilities[t_ts].get(opp['symbol_id'])
    if vol_20 is None:
        continue
    
    threshold = get_decile_threshold(all_volatilities[t_ts])
    if threshold is None:
        continue
    
    if vol_20 >= threshold:
        continue
    
    # Get outcome at T+20
    bar_t = get_bar(opp['symbol_id'], opp['t_ts'])
    bar_t20 = get_bar(opp['symbol_id'], opp['t_plus_20'])
    if not bar_t or not bar_t20:
        continue
    
    forward_return = (bar_t20['close'] - bar_t['close']) / bar_t['close']
    hit = 1 if forward_return > 0 else 0
    
    issued.append({
        'symbol_id': opp['symbol_id'],
        't_ts': opp['t_ts'],
        'hit': hit
    })

if len(issued) < 30:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# Sort by date for holdout
issued.sort(key=lambda x: x['t_ts'])
n_total = len(issued)
n_sealed = int(n_total * 0.2)
sealed_era = issued[-n_sealed:]
train_era = issued[:-n_sealed]

# Count distinct days
days_issued = set(x['t_ts'] for x in issued)
distinct_days = len(days_issued)

# Calculate design effect and effective N
day_counts = defaultdict(int)
for x in issued:
    day_counts[x['t_ts']] += 1
K = len(day_counts)
N = len(issued)
sum_sq = sum(c**2 for c in day_counts.values())
design_effect = (K * sum_sq) / (N * N)
effective_n = N / design_effect

# Calculate metrics
hits_total = sum(x['hit'] for x in issued)
precision_total = hits_total / N if N > 0 else 0
base_rate = hits_total / N if N > 0 else 0

hits_sealed = sum(x['hit'] for x in sealed_era)
precision_sealed = hits_sealed / len(sealed_era) if len(sealed_era) > 0 else 0

print(f"ISSUED={N}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={precision_total:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={precision_sealed:.6f}")

conn.close()