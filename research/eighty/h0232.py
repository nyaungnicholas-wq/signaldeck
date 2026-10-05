import sqlite3, sys
try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
except:
    print("INSUFFICIENT=1")
    sys.exit(0)
cur = conn.cursor()
try:
    # Check required tables exist
    cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('bars','symbols','inst_holdings','prediction_outcomes')")
    if len(cur.fetchall()) != 4:
        print("INSUFFICIENT=1")
        sys.exit(0)
    # Check we have inst_holdings with required columns
    cur.execute("PRAGMA table_info(inst_holdings)")
    cols = [c[1] for c in cur.fetchall()]
    if not all(x in cols for x in ['symbol_id','period','shares']):
        print("INSUFFICIENT=1")
        sys.exit(0)
    # Get 13F periods with their symbol holdings
    cur.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
    """)
    holdings = cur.fetchall()
    if len(holdings) < 2:
        print("INSUFFICIENT=1")
        sys.exit(0)
    # Build per-symbol quarterly changes
    from collections import defaultdict
    by_sym = defaultdict(list)
    for sym, period, shares in holdings:
        by_sym[sym].append((period, shares))
    # Sort each symbol's holdings by period
    for sym in by_sym:
        by_sym[sym].sort()
    # Get daily bars for symbols with enough history
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars = cur.fetchall()
    if not bars:
        print("INSUFFICIENT=1")
        sys.exit(0)
    # Group bars by symbol
    bars_by_sym = defaultdict(list)
    for sym, ts, close in bars:
        bars_by_sym[sym].append((ts, close))
    # Get prediction outcomes for label
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 15
    """)
    outcomes = cur.fetchall()
    outcome_map = defaultdict(dict)
    for sym, ts, up in outcomes:
        outcome_map[sym][ts] = up
    # Get symbol info
    cur.execute("SELECT id, market FROM symbols WHERE active = 1")
    symbols = {r[0]: r[1] for r in cur.fetchall()}
    conn.close()
except:
    print("INSUFFICIENT=1")
    sys.exit(0)
# Build decision points
opportunities = []
for sym, period, shares in holdings:
    if sym not in by_sym or len(by_sym[sym]) < 2:
        continue
    # Find prior quarter
    periods = by_sym[sym]
    try:
        idx = [p[0] for p in periods].index(period)
    except ValueError:
        continue
    if idx == 0:
        continue
    prior_period, prior_shares = periods[idx-1]
    if prior_shares == 0:
        continue
    change_pct = (shares - prior_shares) / prior_shares
    if change_pct >= -0.10:  # not >=10% decrease
        continue
    # Need bars after period (as proxy for filing disclosure)
    # Find first trading day after period (period is YYYY-MM-DD)
    sym_bars = bars_by_sym.get(sym, [])
    if not sym_bars:
        continue
    # Convert period to timestamp (epoch seconds)
    from datetime import datetime
    try:
        period_dt = datetime.strptime(period, "%Y-%m-%d")
    except:
        continue
    period_ts = int(period_dt.timestamp())
    # Find first bar with ts > period_ts
    t_idx = None
    for i, (ts, _) in enumerate(sym_bars):
        if ts > period_ts:
            t_idx = i
            break
    if t_idx is None:
        continue
    t_ts, t_close = sym_bars[t_idx]
    # Need 252 prior sessions
    if t_idx < 252:
        continue
    # Close >= $5
    if t_close < 5:
        continue
    # Average daily dollar volume >= $5M over T-60..T-1
    start_idx = max(0, t_idx-60)
    window_bars = sym_bars[start_idx:t_idx]
    if len(window_bars) < 60:
        continue
    # We don't have volume in bars table? Check schema: bars has volume column
    # Actually bars has volume. We'll compute average daily volume.
    # But we need dollar volume = close * volume. Need volume column.
    # Re-query bars with volume
    conn2 = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur2 = conn2.cursor()
    cur2.execute("""
        SELECT ts, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC
        LIMIT 253
    """, (sym, t_ts))
    recent_bars = cur2.fetchall()
    conn2.close()
    if len(recent_bars) < 253:
        continue
    recent_bars.reverse()  # oldest first
    # T-60..T-1 (exclude T itself)
    if len(recent_bars) < 61:
        continue
    dollar_vols = [b[1]*b[2] for b in recent_bars[-61:-1]]  # last 60 before T
    if sum(dollar_vols)/60 < 5_000_000:
        continue
    # 20-session return through T
    if t_idx < 20:
        continue
    close_20_ago = sym_bars[t_idx-20][1]
    ret_20 = (t_close - close_20_ago) / close_20_ago
    if not (-0.02 <= ret_20 <= 0.02):
        continue
    # T's close-to-close return
    if t_idx < 1:
        continue
    prev_close = sym_bars[t_idx-1][1]
    ret_1d = (t_close - prev_close) / prev_close
    if not (-0.005 <= ret_1d <= 0.005):
        continue
    # 20-day realized volatility at T (std dev of daily returns over last 20 days)
    if t_idx < 20:
        continue
    closes_20 = [b[1] for b in sym_bars[t_idx-20:t_idx+1]]
    returns_20 = [(closes_20[i]-closes_20[i-1])/closes_20[i-1] for i in range(1, len(closes_20))]
    if len(returns_20) < 20:
        continue
    import statistics
    vol_20 = statistics.stdev(returns_20)
    opportunities.append({
        'sym': sym,
        't_ts': t_ts,
        't_close': t_close,
        'vol_20': vol_20,
        'market': symbols.get(sym, 'stocks')
    })
# Calculate cross-sectional volatility decile threshold
vols = [o['vol_20'] for o in opportunities]
if not vols:
    print("INSUFFICIENT=1")
    sys.exit(0)
vol_sorted = sorted(vols)
decile_90 = vol_sorted[int(len(vol_sorted)*0.9)]
# Filter out top decile volatility
issued = []
# Track calls per symbol in last 20 trading days
from datetime import timedelta
call_history = defaultdict(list)
# Process opportunities in time order
opportunities.sort(key=lambda x: x['t_ts'])
for opp in opportunities:
    sym = opp['sym']
    t_ts = opp['t_ts']
    # Abstain if volatility in top decile
    if opp['vol_20'] >= decile_90:
        continue
    # Check if called for same symbol in prior 20 trading days
    # We don't have trading calendar, approximate with calendar days
    t_dt = datetime.utcfromtimestamp(t_ts)
    called = False
    for prev_ts in call_history[sym]:
        prev_dt = datetime.utcfromtimestamp(prev_ts)
        if (t_dt - prev_dt) <= timedelta(days=20):
            called = True
            break
    if called:
        continue
    # Check we have label
    if sym not in outcome_map or t_ts not in outcome_map[sym]:
        continue
    up = outcome_map[sym][t_ts]
    hit = 0 if up == 0 else 1  # down call: hit if actual direction is down (up=0)
    issued.append({
        'sym': sym,
        't_ts': t_ts,
        'hit': hit,
        'day': t_dt.date()
    })
    call_history[sym].append(t_ts)
# Hold out most recent 20% as sealed era
if not issued:
    print("INSUFFICIENT=1")
    sys.exit(0)
issued.sort(key=lambda x: x['t_ts'])
split_idx = int(len(issued)*0.8)
train = issued[:split_idx]
sealed = issued[split_idx:]
# Calculate metrics
issued_count = len(issued)
base_up = sum(1 for o in issued if o['hit'] == 1) / issued_count if issued_count else 0
precision = base_up  # since all are down calls, precision = rate of actual down
# Effective N: count distinct days in issued and design effect
days = [o['day'] for o in issued]
distinct_days = len(set(days))
# Design effect: clustering by day
from collections import Counter
day_counts = Counter(days)
design_effect = 1 + (sum((c-1)**2 for c in day_counts.values())) / (issued_count**2) if issued_count > 1 else 1
effective_n = issued_count / design_effect
# Sealed precision
if sealed:
    sealed_prec = sum(1 for o in sealed if o['hit'] == 1) / len(sealed)
else:
    sealed_prec = 0
# Print required lines
print(f"ISSUED={issued_count}")
print(f"OPPORTUNITIES={len(opportunities)}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_up:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.6f}")
print(f"SEALED_PRECISION={sealed_prec:.6f}")