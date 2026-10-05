# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 785
# cycle_index: 55
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def connect_ro():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def week_start(d):
    return d - timedelta(days=d.weekday())

def add_business_days(d, n):
    # n can be negative
    step = 1 if n >= 0 else -1
    count = 0
    while count < abs(n):
        d += timedelta(days=step)
        if d.weekday() < 5:
            count += 1
    return d

def business_days_between(start, end):
    # count business days from start (exclusive) to end (inclusive)
    if start >= end:
        return 0
    count = 0
    d = start + timedelta(days=1)
    while d <= end:
        if d.weekday() < 5:
            count += 1
        d += timedelta(days=1)
    return count

def check_data_sufficiency(conn):
    cur = conn.cursor()
    
    # Check fundamentals: need quarterly EPS and SharesOutstanding from 2018 onwards
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id) FROM fundamentals 
        WHERE metric IN ('EPS', 'SharesOutstanding') AND as_of > 0
    """)
    fund_symbols = cur.fetchone()[0]
    cur.execute("""
        SELECT MIN(as_of), MAX(as_of) FROM fundamentals 
        WHERE metric IN ('EPS', 'SharesOutstanding') AND as_of > 0
    """)
    fund_range = cur.fetchone()
    
    # Check macro series: need DGS10, DGS2, VIXCLS
    cur.execute("""
        SELECT series, MIN(ts), MAX(ts), COUNT(*) FROM macro_series 
        WHERE series IN ('DGS10', 'DGS2', 'VIXCLS') GROUP BY series
    """)
    macro = {row[0]: (row[1], row[2], row[3]) for row in cur.fetchall()}
    
    # Check insider trades
    cur.execute("SELECT COUNT(*), MIN(filed_ts), MAX(filed_ts) FROM insider_trades WHERE code = 'P'")
    insider_p = cur.fetchone()
    cur.execute("SELECT COUNT(*) FROM insider_trades WHERE code = 'S'")
    insider_s = cur.fetchone()[0]
    
    # Check filings form 4
    cur.execute("SELECT COUNT(*), MIN(filed_ts), MAX(filed_ts) FROM filings WHERE form = '4'")
    filings_4 = cur.fetchone()
    
    # Check bars 1d
    cur.execute("SELECT COUNT(DISTINCT symbol_id), MIN(ts), MAX(ts) FROM bars WHERE tf = '1d'")
    bars_1d = cur.fetchone()
    
    # Check symbols
    cur.execute("SELECT COUNT(*) FROM symbols WHERE active = 1")
    active_symbols = cur.fetchone()[0]
    
    print(f"DEBUG: fundamentals symbols={fund_symbols}, range={fund_range}", file=sys.stderr)
    print(f"DEBUG: macro={macro}", file=sys.stderr)
    print(f"DEBUG: insider purchases={insider_p}, sales={insider_s}", file=sys.stderr)
    print(f"DEBUG: filings form4={filings_4}", file=sys.stderr)
    print(f"DEBUG: bars 1d symbols={bars_1d[0]}, range={bars_1d[1]}..{bars_1d[2]}", file=sys.stderr)
    print(f"DEBUG: active symbols={active_symbols}", file=sys.stderr)
    
    # Fundamentals check: need data from 2018-07 onwards (epoch ~1532476800)
    # The schema shows fundamentals only 2026-07-06..2026-08-14 (fetched_at range)
    # as_of might be historical but only 5,948 rows total for 852 symbols across 6 metrics
    # That's ~1 row per symbol-metric, not quarterly history
    if fund_symbols < 100 or fund_range[0] is None or fund_range[0] > 1532476800:
        return False, "insufficient fundamentals history"
    
    # Macro check
    required_macro = {'DGS10', 'DGS2', 'VIXCLS'}
    if not all(s in macro for s in required_macro):
        return False, "missing macro series"
    for s in required_macro:
        if macro[s][2] < 1000:  # need substantial history
            return False, f"insufficient {s} history"
    
    # Insider trades check
    if insider_p[0] < 100:
        return False, "insufficient insider purchases"
    
    # Filings check
    if filings_4[0] < 100:
        return False, "insufficient form 4 filings"
    
    # Bars check
    if bars_1d[0] < 1000 or bars_1d[1] > 1532476800:
        return False, "insufficient daily bars history"
    
    return True, "ok"

def load_macro_weekly(conn):
    """Load macro series and resample to weekly (Friday close)"""
    cur = conn.cursor()
    cur.execute("SELECT series, ts, value FROM macro_series WHERE series IN ('DGS10', 'DGS2', 'VIXCLS') ORDER BY series, ts")
    rows = cur.fetchall()
    
    by_series = {}
    for series, ts, val in rows:
        d = epoch_to_date(ts)
        ws = week_start(d)
        # Keep last value of week (Friday or last available)
        if series not in by_series:
            by_series[series] = {}
        if ws not in by_series[series] or d > epoch_to_date(max(k for k in by_series[series] if week_start(epoch_to_date(k)) == ws)):
            by_series[series][ts] = val
    
    # Convert to sorted weekly series
    weekly = {}
    for series, data in by_series.items():
        weekly[series] = sorted(data.items())  # list of (ts, value)
    
    return weekly

def compute_spread_rising_weeks(weekly_dgs10, weekly_dgs2):
    """Find weeks where 10Y-2Y spread rose for 5 consecutive weeks ending that week"""
    # Align by week
    dgs10_dict = {week_start(epoch_to_date(ts)): val for ts, val in weekly_dgs10}
    dgs2_dict = {week_start(epoch_to_date(ts)): val for ts, val in weekly_dgs2}
    
    common_weeks = sorted(set(dgs10_dict.keys()) & set(dgs2_dict.keys()))
    if len(common_weeks) < 6:
        return set()
    
    spreads = {w: dgs10_dict[w] - dgs2_dict[w] for w in common_weeks}
    
    rising_weeks = set()
    for i in range(5, len(common_weeks)):
        w = common_weeks[i]
        rising = True
        for j in range(1, 6):
            if spreads[common_weeks[i-j+1]] <= spreads[common_weeks[i-j]]:
                rising = False
                break
        if rising:
            rising_weeks.add(w)
    
    return rising_weeks

def load_fundamentals_quarterly(conn):
    """Load quarterly EPS and SharesOutstanding, keyed by symbol_id and as_of (quarter end)"""
    cur = conn.cursor()
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at 
        FROM fundamentals 
        WHERE metric IN ('EPS', 'SharesOutstanding') AND as_of > 0
        ORDER BY symbol_id, as_of, fetched_at
    """)
    rows = cur.fetchall()
    
    # For each symbol_id and as_of, take the latest fetched_at (most recent knowledge)
    by_symbol = {}
    for symbol_id, metric, value, as_of, fetched_at in rows:
        if symbol_id not in by_symbol:
            by_symbol[symbol_id] = {}
        if as_of not in by_symbol[symbol_id]:
            by_symbol[symbol_id][as_of] = {'EPS': None, 'SharesOutstanding': None, 'fetched_at': 0}
        if fetched_at > by_symbol[symbol_id][as_of]['fetched_at']:
            by_symbol[symbol_id][as_of][metric] = value
            by_symbol[symbol_id][as_of]['fetched_at'] = fetched_at
    
    # Filter to only quarters where both EPS and SharesOutstanding exist
    result = {}
    for symbol_id, quarters in by_symbol.items():
        result[symbol_id] = {}
        for as_of, data in quarters.items():
            if data['EPS'] is not None and data['SharesOutstanding'] is not None and data['SharesOutstanding'] > 0:
                result[symbol_id][as_of] = (data['EPS'], data['SharesOutstanding'], data['fetched_at'])
    
    return result

def load_insider_officer_purchases(conn):
    """Load open-market purchases by CEO/CFO with Form 4 filing info"""
    cur = conn.cursor()
    # Join insider_trades with filings on symbol_id and filed_ts proximity
    # insider_trades has accession which matches filings
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code, it.shares, it.price, it.value, 
               it.tx_ts, it.filed_ts, f.form, f.filed_ts as filing_filed_ts
        FROM insider_trades it
        LEFT JOIN filings f ON it.accession = f.id
        WHERE it.code = 'P' 
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
        ORDER BY it.symbol_id, it.filed_ts
    """)
    rows = cur.fetchall()
    
    purchases = []
    for row in rows:
        accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts, form, filing_filed_ts = row
        # Use insider_trades filed_ts as primary (when it became public)
        # Verify Form 4
        if form != '4':
            continue
        # Disclosure delay: filed_ts - tx_ts <= 5 business days
        tx_date = epoch_to_date(tx_ts)
        filed_date = epoch_to_date(filed_ts)
        delay = business_days_between(tx_date, filed_date)
        if delay > 5:
            continue
        purchases.append({
            'symbol_id': symbol_id,
            'insider': insider,
            'title': title,
            'tx_ts': tx_ts,
            'filed_ts': filed_ts,
            'tx_date': tx_date,
            'filed_date': filed_date,
            'shares': shares,
            'price': price,
            'value': value
        })
    
    return purchases

def load_insider_sales_by_officer(conn):
    """Load open-market sales by officer for 63-session lookback check"""
    cur = conn.cursor()
    cur.execute("""
        SELECT symbol_id, insider, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'S'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY symbol_id, insider, tx_ts
    """)
    rows = cur.fetchall()
    
    by_symbol_insider = {}
    for symbol_id, insider, tx_ts, filed_ts in rows:
        key = (symbol_id, insider)
        if key not in by_symbol_insider:
            by_symbol_insider[key] = []
        by_symbol_insider[key].append((tx_ts, filed_ts))
    
    return by_symbol_insider

def load_bars_daily(conn, symbol_ids):
    """Load daily bars for given symbols"""
    cur = conn.cursor()
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close FROM bars 
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    rows = cur.fetchall()
    
    by_symbol = {}
    for symbol_id, ts, close in rows:
        if symbol_id not in by_symbol:
            by_symbol[symbol_id] = []
        by_symbol[symbol_id].append((ts, close))
    
    return by_symbol

def load_vix_daily(conn):
    """Load VIX daily values"""
    cur = conn.cursor()
    cur.execute("SELECT ts, value FROM macro_series WHERE series = 'VIXCLS' ORDER BY ts")
    return {epoch_to_date(ts): val for ts, val in cur.fetchall()}

def get_latest_quarterly_fundamental(fundamentals, symbol_id, as_of_date):
    """Get latest quarterly fundamental known as of as_of_date (fetched_at <= as_of_date)"""
    if symbol_id not in fundamentals:
        return None
    best = None
    best_fetched = 0
    for as_of, (eps, shares, fetched_at) in fundamentals[symbol_id].items():
        if fetched_at <= as_of_date and fetched_at > best_fetched:
            best = (eps, shares, as_of)
            best_fetched = fetched_at
    return best

def get_price_on_date(bars, symbol_id, target_date):
    """Get close price on or before target_date"""
    if symbol_id not in bars:
        return None
    target_ts = date_to_epoch(target_date)
    best = None
    for ts, close in bars[symbol_id]:
        if ts <= target_ts:
            best = close
        else:
            break
    return best

def get_forward_return_21d(bars, symbol_id, entry_date):
    """Get 21-trading-day forward return from entry_date"""
    if symbol_id not in bars:
        return None
    entry_ts = date_to_epoch(entry_date)
    # Find entry bar index
    entry_idx = -1
    for i, (ts, close) in enumerate(bars[symbol_id]):
        if ts >= entry_ts:
            entry_idx = i
            break
    if entry_idx == -1 or entry_idx + 21 >= len(bars[symbol_id]):
        return None
    entry_price = bars[symbol_id][entry_idx][1]
    exit_price = bars[symbol_id][entry_idx + 21][1]
    return (exit_price - entry_price) / entry_price

def compute_design_effect(issued_dates):
    """Compute design effect from day clustering"""
    if len(issued_dates) <= 1:
        return 1.0
    # Simple design effect: 1 + (avg cluster size - 1) * ICC
    # Approximate: count calls per day, design_effect = 1 + (mean_cluster_size - 1) * 0.5
    from collections import Counter
    day_counts = Counter(issued_dates)
    mean_cluster = sum(day_counts.values()) / len(day_counts)
    # Conservative ICC of 0.3 for financial returns
    icc = 0.3
    deff = 1 + (mean_cluster - 1) * icc
    return deff

def main():
    conn = connect_ro()
    
    # Check data sufficiency
    sufficient, reason = check_data_sufficiency(conn)
    if not sufficient:
        print("INSUFFICIENT=1")
        return 0
    
    # Load all data
    print("Loading macro...", file=sys.stderr)
    weekly_macro = load_macro_weekly(conn)
    rising_spread_weeks = compute_spread_rising_weeks(weekly_macro['DGS10'], weekly_macro['DGS2'])
    print(f"Rising spread weeks: {len(rising_spread_weeks)}", file=sys.stderr)
    
    print("Loading fundamentals...", file=sys.stderr)
    fundamentals = load_fundamentals_quarterly(conn)
    print(f"Fundamentals symbols: {len(fundamentals)}", file=sys.stderr)
    
    print("Loading insider purchases...", file=sys.stderr)
    purchases = load_insider_officer_purchases(conn)
    print(f"Officer purchases: {len(purchases)}", file=sys.stderr)
    
    print("Loading insider sales...", file=sys.stderr)
    sales_by_officer = load_insider_sales_by_officer(conn)
    
    print("Loading VIX...", file=sys.stderr)
    vix_daily = load_vix_daily(conn)
    
    # Get all symbol_ids that have purchases
    purchase_symbols = set(p['symbol_id'] for p in purchases)
    
    print("Loading bars...", file=sys.stderr)
    bars = load_bars_daily(conn, list(purchase_symbols))
    print(f"Bars symbols: {len(bars)}", file=sys.stderr)
    
    # Get 10Y yield weekly for earnings yield comparison
    dgs10_weekly = {week_start(epoch_to_date(ts)): val for ts, val in weekly_macro['DGS10']}
    
    # Generate signals
    opportunities = 0
    issued_calls = []  # list of (filed_date, symbol_id, hit, insider)
    
    for p in purchases:
        opportunities += 1
        symbol_id = p['symbol_id']
        filed_date = p['filed_date']
        insider = p['insider']
        
        # Condition (a): 10Y-2Y spread rose for 5 consecutive weeks ending this week
        filed_week = week_start(filed_date)
        if filed_week not in rising_spread_weeks:
            continue
        
        # Condition (b): earnings yield > 10Y yield + 200bps
        # Need latest quarterly fundamental known as of filed_date
        fund = get_latest_quarterly_fundamental(fundamentals, symbol_id, p['filed_ts'])
        if not fund:
            continue
        eps, shares, fund_as_of = fund
        # Get price on filed_date
        price = get_price_on_date(bars, symbol_id, filed_date)
        if not price or price <= 0:
            continue
        earnings_yield = eps / price  # EPS is per share
        dgs10 = dgs10_weekly.get(filed_week)
        if dgs10 is None:
            continue
        if earnings_yield < dgs10 + 0.02:  # 200bps = 0.02
            continue
        
        # Condition (c): already satisfied by purchase filter (officer, open-market, Form 4, <=5 day delay)
        
        # Abstain: VIX > 30 on disclosure date
        vix = vix_daily.get(filed_date)
        if vix is not None and vix > 30:
            continue
        
        # Abstain: same officer sold open-market in prior 63 sessions
        key = (symbol_id, insider)
        if key in sales_by_officer:
            sale_dates = [epoch_to_date(tx_ts) for tx_ts, _ in sales_by_officer[key]]
            # Check if any sale in 63 trading days before filed_date
            # Approximate: 63 trading days ~ 89 calendar days
            cutoff = filed_date - timedelta(days=89)
            if any(sd >= cutoff and sd < filed_date for sd in sale_dates):
                continue
        
        # All conditions met - issue call
        fwd_ret = get_forward_return_21d(bars, symbol_id, filed_date)
        if fwd_ret is None:
            continue
        hit = 1 if fwd_ret > 0 else 0
        issued_calls.append((filed_date, symbol_id, hit, insider))
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by date
    issued_calls.sort(key=lambda x: x[0])
    
    # Split: most recent 20% as sealed era
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_calls = issued_calls[-n_sealed:]
    main_calls = issued_calls[:-n_sealed]
    
    # Check sealed era independent observations (distinct days)
    sealed_days = set(c[0] for c in sealed_calls)
    if len(sealed_days) < 30:
        print("INSUFFICIENT=1")
        return 0
    
    # Compute metrics
    issued = len(issued_calls)
    hits = sum(c[2] for c in issued_calls)
    precision = hits / issued if issued > 0 else 0
    
    # Base rate within issued subset
    base_rate = hits / issued if issued > 0 else 0
    
    distinct_days = len(set(c[0] for c in issued_calls))
    
    # Design effect and effective N
    deff = compute_design_effect([c[0] for c in issued_calls])
    effective_n = issued / deff
    
    # Sealed precision
    sealed_hits = sum(c[2] for c in sealed_calls)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0
    
    # Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())