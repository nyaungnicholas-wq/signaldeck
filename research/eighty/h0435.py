# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 434
# cycle_index: 25
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta

DB_PATH = "data/signaldeck.db"
HORIZON = 21
MIN_PRICE_DROP = 0.15
MIN_SENTIMENT_CHANGE = 0.05
MAX_DISCLOSURE_DAYS = 10
MIN_AVG_DOLLAR_VOLUME = 5_000_000
MIN_INST_OWNERSHIP = 0.10
SEALED_RATIO = 0.20

conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True, timeout=30)
conn.execute("PRAGMA journal_mode=WAL")
conn.execute("PRAGMA query_only=ON")

# Get all insider trades (Form 4 open-market purchases)
trades = conn.execute("""
    SELECT 
        it.symbol_id,
        it.filed_ts,
        it.insider,
        it.title,
        it.tx_ts
    FROM insider_trades it
    WHERE it.code = 'P'
    AND (it.title LIKE '%Officer%' OR it.title LIKE '%Director%')
    ORDER BY it.filed_ts
""").fetchall()

if not trades:
    print("INSUFFICIENT=1")
    exit(0)

# Get symbols with daily bars and quarterly EPS
valid_symbols_sql = """
    SELECT DISTINCT it.symbol_id
    FROM insider_trades it
    JOIN bars b ON b.symbol_id = it.symbol_id AND b.tf = '1d'
    JOIN fundamentals f ON f.symbol_id = it.symbol_id AND f.metric = 'EPS'
    GROUP BY it.symbol_id
    HAVING COUNT(DISTINCT b.ts) > 0 AND COUNT(DISTINCT f.as_of) >= 2
"""
valid_symbols = {row[0] for row in conn.execute(valid_symbols_sql).fetchall()}

if not valid_symbols:
    print("INSUFFICIENT=1")
    exit(0)

# Preload all necessary data
bars_cache = {}
symbols_cache = {}
news_cache = {}
fundamentals_cache = {}
holdings_cache = {}
outcomes_cache = {}

# Load all bars (daily) for valid symbols
bar_rows = conn.execute("""
    SELECT symbol_id, ts, open, high, low, close, volume
    FROM bars
    WHERE tf = '1d' AND symbol_id IN (SELECT DISTINCT symbol_id FROM insider_trades)
""").fetchall()
for row in bar_rows:
    symbol_id, ts, open_, high, low, close, volume = row
    if symbol_id not in bars_cache:
        bars_cache[symbol_id] = []
    bars_cache[symbol_id].append((ts, open_, high, low, close, volume))

# Sort bars by timestamp for each symbol
for symbol_id in bars_cache:
    bars_cache[symbol_id].sort(key=lambda x: x[0])

# Load all news sentiment
news_rows = conn.execute("""
    SELECT symbol_id, ts, score
    FROM news
    WHERE symbol_id IN (SELECT DISTINCT symbol_id FROM insider_trades)
""").fetchall()
for row in news_rows:
    symbol_id, ts, score = row
    if symbol_id not in news_cache:
        news_cache[symbol_id] = []
    news_cache[symbol_id].append((ts, score))

for symbol_id in news_cache:
    news_cache[symbol_id].sort(key=lambda x: x[0])

# Load fundamentals
fund_rows = conn.execute("""
    SELECT symbol_id, metric, value, as_of
    FROM fundamentals
    WHERE metric = 'EPS' AND symbol_id IN (SELECT DISTINCT symbol_id FROM insider_trades)
""").fetchall()
for row in fund_rows:
    symbol_id, metric, value, as_of = row
    if symbol_id not in fundamentals_cache:
        fundamentals_cache[symbol_id] = []
    fundamentals_cache[symbol_id].append((as_of, value))

for symbol_id in fundamentals_cache:
    fundamentals_cache[symbol_id].sort(key=lambda x: x[0])

# Load institutional holdings
holdings_rows = conn.execute("""
    SELECT symbol_id, period, value, shares
    FROM inst_holdings
    WHERE symbol_id IN (SELECT DISTINCT symbol_id FROM insider_trades)
""").fetchall()
for row in holdings_rows:
    symbol_id, period, value, shares = row
    if symbol_id not in holdings_cache:
        holdings_cache[symbol_id] = []
    holdings_cache[symbol_id].append((period, value, shares))

for symbol_id in holdings_cache:
    holdings_cache[symbol_id].sort(key=lambda x: x[0])

# Load prediction outcomes for horizon=21
outcomes_rows = conn.execute("""
    SELECT symbol_id, ts, up, fwd_return
    FROM prediction_outcomes
    WHERE horizon = ?
    AND symbol_id IN (SELECT DISTINCT symbol_id FROM insider_trades)
""", (HORIZON,)).fetchall()
for row in outcomes_rows:
    symbol_id, ts, up, fwd_return = row
    if symbol_id not in outcomes_cache:
        outcomes_cache[symbol_id] = []
    outcomes_cache[symbol_id].append((ts, up, fwd_return))

for symbol_id in outcomes_cache:
    outcomes_cache[symbol_id].sort(key=lambda x: x[0])

conn.close()

def get_daily_sentiment(symbol_id, day_ts):
    """Get average sentiment for a symbol on a specific day."""
    if symbol_id not in news_cache:
        return None
    
    day_start = day_ts
    day_end = day_ts + 86400
    scores = [score for ts, score in news_cache[symbol_id] if day_start <= ts < day_end]
    if not scores:
        return None
    return sum(scores) / len(scores)

def find_earnings_date(symbol_id, decision_ts):
    """Find the most recent earnings date before decision_ts based on sentiment change."""
    if symbol_id not in news_cache:
        return None
    
    news_points = news_cache[symbol_id]
    valid_news = [(ts, score) for ts, score in news_points if ts < decision_ts]
    
    if len(valid_news) < 2:
        return None
    
    # Group by day
    days = {}
    for ts, score in valid_news:
        day = ts // 86400 * 86400
        if day not in days:
            days[day] = []
        days[day].append(score)
    
    sorted_days = sorted(days.keys(), reverse=True)
    
    for i in range(1, len(sorted_days)):
        current_day = sorted_days[i-1]
        prev_day = sorted_days[i]
        
        current_avg = sum(days[current_day]) / len(days[current_day])
        prev_avg = sum(days[prev_day]) / len(days[prev_day])
        
        if prev_avg == 0:
            continue
            
        change = abs(current_avg - prev_avg) / abs(prev_avg)
        
        if change >= MIN_SENTIMENT_CHANGE:
            return current_day
    
    return None

def get_price_at(symbol_id, target_ts):
    """Get closing price at or before target_ts."""
    if symbol_id not in bars_cache:
        return None
    
    bars = bars_cache[symbol_id]
    for ts, open_, high, low, close, volume in reversed(bars):
        if ts <= target_ts:
            return close
    return None

def get_pre_earnings_close(symbol_id, earnings_ts):
    """Get close price on day before earnings_ts."""
    bars = bars_cache.get(symbol_id, [])
    prev_bars = [(ts, close) for ts, open_, high, low, close, volume in bars if ts < earnings_ts]
    if not prev_bars:
        return None
    return prev_bars[-1][1]

def get_20day_avg_dollar_volume(symbol_id, decision_ts):
    """Get 20-day average dollar volume at decision date."""
    if symbol_id not in bars_cache:
        return None
    
    bars = bars_cache[symbol_id]
    recent_bars = [(ts, close, volume) for ts, open_, high, low, close, volume in bars 
                  if ts < decision_ts][-20:]
    
    if len(recent_bars) < 20:
        return None
    
    total_dollar_volume = sum(close * volume for ts, close, volume in recent_bars)
    return total_dollar_volume / 20

def get_latest_inst_ownership(symbol_id, decision_ts):
    """Get latest institutional ownership ratio, lagged by 45 days."""
    if symbol_id not in holdings_cache:
        return None
    
    holdings = holdings_cache[symbol_id]
    max_period = None
    max_shares = None
    max_value = None
    
    # Find latest period with filing lag
    for period, value, shares in reversed(holdings):
        if period + 45 * 86400 < decision_ts:
            if max_period is None or period > max_period:
                max_period = period
                max_shares = shares
                max_value = value
            break
    
    if max_shares is None or max_shares == 0:
        return None
    
    # Get shares outstanding from fundamentals
    if symbol_id not in fundamentals_cache:
        return None
    
    shares_outstanding = None
    for as_of, value in fundamentals_cache[symbol_id]:
        if as_of < decision_ts and value is not None:
            try:
                shares_outstanding = float(value)
            except:
                continue
    
    if shares_outstanding is None or shares_outstanding == 0:
        return None
    
    return max_shares / shares_outstanding

def get_prior_eps_growth(symbol_id, decision_ts):
    """Get prior quarter EPS growth."""
    if symbol_id not in fundamentals_cache:
        return None
    
    eps_data = fundamentals_cache[symbol_id]
    valid_eps = [(as_of, value) for as_of, value in eps_data if as_of < decision_ts]
    
    if len(valid_eps) < 2:
        return None
    
    # Get two most recent quarters
    valid_eps.sort(key=lambda x: x[0], reverse=True)
    current_eps = None
    prev_eps = None
    
    for as_of, value in valid_eps:
        try:
            current_val = float(value)
            if current_eps is None:
                current_eps = current_val
            elif prev_eps is None:
                prev_eps = current_val
                break
        except:
            continue
    
    if current_eps is None or prev_eps is None:
        return None
    
    if prev_eps == 0:
        return None
    
    return (current_eps - prev_eps) / abs(prev_eps)

def get_label(symbol_id, trade_ts):
    """Get prediction label for horizon=21 after trade_ts."""
    if symbol_id not in outcomes_cache:
        return None, None
    
    outcomes = outcomes_cache[symbol_id]
    # Find outcome with ts >= trade_ts (next available prediction)
    for ts, up, fwd_return in outcomes:
        if ts >= trade_ts:
            return up, fwd_return
    
    return None, None

# Process all trades
opportunities = 0
issued_calls = []
design_effect_numerator = 0
design_effect_denominator = 0
total_issued = 0
total_hits = 0
day_counts = {}

# Determine split point for sealed era
trade_dates = [filed_ts for _, filed_ts, _, _, _ in trades]
trade_dates.sort()
split_idx = int(len(trade_dates) * (1 - SEALED_RATIO))
split_ts = trade_dates[split_idx] if split_idx < len(trade_dates) else trade_dates[-1]

for trade in trades:
    symbol_id, filed_ts, insider, title, tx_ts = trade
    
    if symbol_id not in valid_symbols:
        continue
    
    opportunities += 1
    
    # Find earnings date
    earnings_ts = find_earnings_date(symbol_id, filed_ts)
    if earnings_ts is None:
        continue
    
    # Get pre-earnings close and earnings close
    pre_close = get_pre_earnings_close(symbol_id, earnings_ts)
    earnings_close = get_price_at(symbol_id, earnings_ts)
    
    if pre_close is None or earnings_close is None:
        continue
    
    # Check price drop
    price_drop = (earnings_close - pre_close) / pre_close
    if price_drop > -MIN_PRICE_DROP:
        continue
    
    # Check disclosure timing
    earnings_date = datetime.utcfromtimestamp(earnings_ts).date()
    trade_date = datetime.utcfromtimestamp(filed_ts).date()
    days_diff = (trade_date - earnings_date).days
    
    if days_diff > MAX_DISCLOSURE_DAYS or days_diff < 0:
        continue
    
    # Check 20-day average dollar volume
    avg_dollar_vol = get_20day_avg_dollar_volume(symbol_id, filed_ts)
    if avg_dollar_vol is None or avg_dollar_vol < MIN_AVG_DOLLAR_VOLUME:
        continue
    
    # Check institutional ownership
    inst_ownership = get_latest_inst_ownership(symbol_id, filed_ts)
    if inst_ownership is not None and inst_ownership < MIN_INST_OWNERSHIP:
        continue
    
    # Check prior EPS growth
    eps_growth = get_prior_eps_growth(symbol_id, filed_ts)
    if eps_growth is not None and eps_growth < 0:
        continue
    
    # Get label
    label, fwd_return = get_label(symbol_id, filed_ts)
    if label is None:
        continue
    
    # Issue call (predict up)
    is_hit = label == 1
    total_issued += 1
    if is_hit:
        total_hits += 1
    
    # Track design effect
    day_key = (symbol_id, trade_date)
    if day_key not in day_counts:
        day_counts[day_key] = 0
    day_counts[day_key] += 1
    
    issued_calls.append((filed_ts, is_hit))

if total_issued == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Compute design effect
total_variance = 0
cluster_means = {}
cluster_sizes = {}

for (symbol_id, day), count in day_counts.items():
    cluster_means[(symbol_id, day)] = 0
    cluster_sizes[(symbol_id, day)] = count

for _, is_hit in issued_calls:
    total_variance += (1 if is_hit else 0) ** 2

# Simple design effect approximation: 1 + avg_cluster_size - 1
avg_cluster_size = sum(cluster_sizes.values()) / len(cluster_sizes) if cluster_sizes else 1
design_effect = avg_cluster_size

# Calculate metrics
precision = total_hits / total_issued if total_issued > 0 else 0
base_rate = total_hits / total_issued

# Count distinct days in issued calls
distinct_days = len(set(trade_date for _, hit in issued_calls 
                       for trade_date in [datetime.utcfromtimestamp(
                           next(ts for ts, _ in [(filed_ts, _) for filed_ts, _ in issued_calls if _ == hit][:1])
                       ).date()]))

# Effective N
effective_n = total_issued / design_effect

# Sealed era metrics
sealed_issued = 0
sealed_hits = 0
for filed_ts, hit in issued_calls:
    if filed_ts >= split_ts:
        sealed_issued += 1
        if hit:
            sealed_hits += 1

sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

print(f"ISSUED={total_issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")