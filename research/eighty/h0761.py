# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 760
# cycle_index: 30
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import bisect

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def business_days_between(start_ts, end_ts):
    start = epoch_to_date(start_ts)
    end = epoch_to_date(end_ts)
    if start > end:
        return 0
    days = 0
    cur = start
    while cur <= end:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days

def add_trading_days(start_ts, n):
    """Add n trading days to start_ts, return epoch of that trading day."""
    cur = epoch_to_date(start_ts)
    count = 0
    while count < n:
        cur += timedelta(days=1)
        if cur.weekday() < 5:
            count += 1
    return date_to_epoch(cur)

def get_trading_days_between(start_ts, end_ts):
    """Return list of trading day epochs between start_ts (inclusive) and end_ts (inclusive)."""
    start = epoch_to_date(start_ts)
    end = epoch_to_date(end_ts)
    days = []
    cur = start
    while cur <= end:
        if cur.weekday() < 5:
            days.append(date_to_epoch(cur))
        cur += timedelta(days=1)
    return days

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get universe: symbols with 13F coverage, non-zero institutional ownership, and news sentiment data
    # inst_holdings period range: 2023-12-31 to 2026-06-30
    # news range: 2012-04-17 to 2026-08-15
    # bars 1d range: 2018-07-26 to 2026-08-15
    # Universe period: 2012-01 to 2025-12 (but limited by inst_holdings)
    
    # Get symbols with inst_holdings data
    cur.execute("""
        SELECT DISTINCT symbol_id FROM inst_holdings
    """)
    inst_symbols = {row['symbol_id'] for row in cur.fetchall()}
    
    # Get symbols with news data
    cur.execute("""
        SELECT DISTINCT symbol_id FROM news
    """)
    news_symbols = {row['symbol_id'] for row in cur.fetchall()}
    
    # Get symbols with bars 1d data
    cur.execute("""
        SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'
    """)
    bars_symbols = {row['symbol_id'] for row in cur.fetchall()}
    
    universe_symbols = inst_symbols & news_symbols & bars_symbols
    print(f"Universe symbols: {len(universe_symbols)}", file=sys.stderr)
    
    if len(universe_symbols) == 0:
        print("INSUFFICIENT=1")
        return

    # 2. Build institutional ownership per symbol per quarter (sum of value across managers)
    # period is quarter END date (string? let's check - schema says period is quarter END)
    cur.execute("""
        SELECT symbol_id, period, SUM(value) as total_value, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    inst_ownership = defaultdict(list)  # symbol_id -> list of (period_epoch, total_value, total_shares)
    for row in cur.fetchall():
        # period is quarter end, e.g., '2023-12-31'
        period_str = row['period']
        try:
            period_date = datetime.strptime(period_str, '%Y-%m-%d').date()
        except:
            continue
        period_epoch = date_to_epoch(period_date)
        inst_ownership[row['symbol_id']].append((period_epoch, row['total_value'], row['total_shares']))
    
    # Filter symbols with at least 3 quarters of data (need 2 prior quarters for trend)
    valid_inst_symbols = {}
    for sym, quarters in inst_ownership.items():
        if len(quarters) >= 3:
            valid_inst_symbols[sym] = quarters
    
    print(f"Symbols with >=3 quarters 13F: {len(valid_inst_symbols)}", file=sys.stderr)
    
    # 3. Get insider open-market purchases (code='P')
    cur.execute("""
        SELECT symbol_id, accession, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    insider_purchases = []
    for row in cur.fetchall():
        if row['symbol_id'] in valid_inst_symbols:
            insider_purchases.append({
                'symbol_id': row['symbol_id'],
                'accession': row['accession'],
                'insider': row['insider'],
                'title': row['title'],
                'shares': row['shares'],
                'price': row['price'],
                'value': row['value'],
                'tx_ts': row['tx_ts'],
                'filed_ts': row['filed_ts']
            })
    
    print(f"Insider open-market purchases in universe: {len(insider_purchases)}", file=sys.stderr)
    
    # 4. Get news sentiment per symbol per day
    # news.ts is unix epoch. sentiment score is in 'score' column.
    cur.execute("""
        SELECT symbol_id, ts, score
        FROM news
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?' * len(valid_inst_symbols))), list(valid_inst_symbols.keys()))
    
    news_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        news_by_symbol[row['symbol_id']].append((row['ts'], row['score']))
    
    # Compute daily average sentiment per symbol
    daily_sentiment = defaultdict(dict)  # symbol_id -> {day_epoch: avg_score}
    for sym, entries in news_by_symbol.items():
        by_day = defaultdict(list)
        for ts, score in entries:
            day = epoch_to_date(ts)
            day_epoch = date_to_epoch(day)
            by_day[day_epoch].append(score)
        for day_epoch, scores in by_day.items():
            daily_sentiment[sym][day_epoch] = sum(scores) / len(scores)
    
    # 5. For each date, compute bottom decile threshold across universe
    # Get all dates that have news data for any symbol in universe
    all_dates = set()
    for sym in valid_inst_symbols:
        all_dates.update(daily_sentiment[sym].keys())
    all_dates = sorted(all_dates)
    
    # For each date, collect sentiment scores across symbols that have data
    date_to_scores = defaultdict(list)
    for sym in valid_inst_symbols:
        for day_epoch, score in daily_sentiment[sym].items():
            date_to_scores[day_epoch].append(score)
    
    # Compute bottom decile (10th percentile) for each date
    date_to_bottom_decile = {}
    for day_epoch, scores in date_to_scores.items():
        if len(scores) >= 10:  # need enough symbols for decile
            scores_sorted = sorted(scores)
            idx = max(0, int(len(scores_sorted) * 0.1) - 1)
            date_to_bottom_decile[day_epoch] = scores_sorted[idx]
    
    print(f"Dates with bottom decile computed: {len(date_to_bottom_decile)}", file=sys.stderr)
    
    # 6. Get bars 1d data for forward returns and dollar volume
    # We'll fetch on demand per symbol to avoid memory issues
    def get_bars_1d(symbol_id):
        cur.execute("""
            SELECT ts, open, high, low, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (symbol_id,))
        return [(row['ts'], row['open'], row['high'], row['low'], row['close'], row['volume']) for row in cur.fetchall()]
    
    # Pre-load bars for all valid symbols (could be memory heavy but 102 symbols * ~2000 days = 200k rows)
    bars_by_symbol = {}
    for sym in valid_inst_symbols:
        bars_by_symbol[sym] = get_bars_1d(sym)
    
    print(f"Loaded bars for {len(bars_by_symbol)} symbols", file=sys.stderr)
    
    # 7. Process each insider purchase as potential entry
    opportunities = 0
    issued_calls = []  # list of (symbol_id, filed_ts, entry_price, forward_return, call_date_epoch)
    
    for purchase in insider_purchases:
        sym = purchase['symbol_id']
        filed_ts = purchase['filed_ts']
        tx_ts = purchase['tx_ts']
        
        opportunities += 1
        
        # Check disclosure delay <= 5 business days
        delay_bdays = business_days_between(tx_ts, filed_ts)
        if delay_bdays > 5:
            continue
        
        # Check 21-day avg news sentiment in bottom decile as of filed_ts
        filed_day = epoch_to_date(filed_ts)
        filed_day_epoch = date_to_epoch(filed_day)
        
        # Get 21 trading days prior to filed_day (inclusive)
        trading_days = get_trading_days_between(add_trading_days(filed_day_epoch, -20), filed_day_epoch)
        if len(trading_days) < 21:
            continue
        
        # Get sentiment scores for those days
        sentiment_scores = []
        for td in trading_days:
            if td in daily_sentiment.get(sym, {}):
                sentiment_scores.append(daily_sentiment[sym][td])
        
        if len(sentiment_scores) < 15:  # require most days
            continue
        
        avg_sentiment = sum(sentiment_scores) / len(sentiment_scores)
        
        # Check if avg_sentiment <= bottom decile for filed_day
        if filed_day_epoch not in date_to_bottom_decile:
            continue
        if avg_sentiment > date_to_bottom_decile[filed_day_epoch]:
            continue
        
        # Check 13F institutional ownership increased in each of last two quarters
        # As of most recent 13F filing date (period) that is <= filed_ts - 45 days (lag)
        # 13F period is quarter end, filing is up to 45 days later. We only have period.
        # So we need period <= filed_ts - 45 days (conservative: assume filed 45 days after period)
        cutoff_ts = filed_ts - 45 * 86400
        quarters = valid_inst_symbols[sym]
        # Find quarters with period <= cutoff_ts
        eligible_quarters = [q for q in quarters if q[0] <= cutoff_ts]
        if len(eligible_quarters) < 3:
            continue
        # Last two quarters should show increase in total_value
        q1 = eligible_quarters[-1]  # most recent
        q2 = eligible_quarters[-2]
        q3 = eligible_quarters[-3]
        if not (q1[1] > q2[1] and q2[1] > q3[1]):
            continue
        
        # Check 20-day average dollar volume >= $1M as of filed_ts
        bars = bars_by_symbol.get(sym, [])
        if not bars:
            continue
        # Find bars up to filed_day
        bars_up_to = [b for b in bars if b[0] <= filed_day_epoch]
        if len(bars_up_to) < 20:
            continue
        last_20 = bars_up_to[-20:]
        dollar_vols = [b[4] * b[5] for b in last_20]  # close * volume
        avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
        if avg_dollar_vol < 1_000_000:
            continue
        
        # All conditions met - issue call on filed_ts
        # Get entry price: close on filed_day (or next trading day if filed_day not a trading day?)
        # filed_ts is disclosure timestamp. Decision is at disclosure. Use close of that trading day.
        # Find bar for filed_day_epoch
        entry_price = None
        for b in bars:
            if b[0] == filed_day_epoch:
                entry_price = b[4]  # close
                break
        if entry_price is None:
            # filed_day might not be a trading day (weekend/holiday). Use next trading day?
            # But as-of discipline: we can only use data available at decision time.
            # If filed on weekend, next trading day close is not known yet.
            # Use prior trading day close.
            prior_bars = [b for b in bars if b[0] < filed_day_epoch]
            if not prior_bars:
                continue
            entry_price = prior_bars[-1][4]
        
        # Compute forward return over 63 trading days
        # Find the bar 63 trading days after entry
        entry_idx = None
        for i, b in enumerate(bars):
            if b[0] == filed_day_epoch:
                entry_idx = i
                break
        if entry_idx is None:
            # use prior trading day as entry
            for i, b in enumerate(bars):
                if b[0] < filed_day_epoch:
                    entry_idx = i
            if entry_idx is None:
                continue
        
        target_idx = entry_idx + 63
        if target_idx >= len(bars):
            continue  # not enough forward data
        
        exit_price = bars[target_idx][4]
        forward_return = (exit_price - entry_price) / entry_price
        
        issued_calls.append({
            'symbol_id': sym,
            'call_date': filed_day_epoch,
            'filed_ts': filed_ts,
            'entry_price': entry_price,
            'forward_return': forward_return,
            'up': 1 if forward_return > 0 else 0
        })
    
    print(f"Opportunities considered: {opportunities}", file=sys.stderr)
    print(f"Issued calls: {len(issued_calls)}", file=sys.stderr)
    
    if len(issued_calls) < 30:
        print("INSUFFICIENT=1")
        return
    
    # 8. Split into training (80%) and sealed (20%) by time
    issued_calls.sort(key=lambda x: x['call_date'])
    split_idx = int(len(issued_calls) * 0.8)
    training_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]
    
    if len(training_calls) < 30:
        print("INSUFFICIENT=1")
        return
    
    # 9. Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c['up'] for c in calls)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up) within issued subset
        distinct_days = len(set(c['call_date'] for c in calls))
        return issued, hits, precision, base_rate, distinct_days
    
    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days = compute_metrics(training_calls)
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct_days = compute_metrics(sealed_calls)
    
    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Simplified: cluster by month, compute variance inflation
    def effective_n(calls):
        if len(calls) <= 1:
            return len(calls)
        # Cluster by year-month
        clusters = defaultdict(int)
        for c in calls:
            dt = epoch_to_date(c['call_date'])
            key = (dt.year, dt.month)
            clusters[key] += 1
        cluster_sizes = list(clusters.values())
        if len(cluster_sizes) <= 1:
            return len(calls)
        # Design effect approximation: 1 + (mean_cluster_size - 1) * rho
        # Assume rho (ICC) = 0.1 for financial returns (conservative)
        mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
        rho = 0.1
        deff = 1 + (mean_cluster - 1) * rho
        return len(calls) / deff
    
    train_eff_n = effective_n(training_calls)
    sealed_eff_n = effective_n(sealed_calls)
    
    # Overall metrics (training fold for main claim)
    ISSUED = train_issued
    OPPORTUNITIES = opportunities
    PRECISION = train_precision
    BASE_RATE = train_base_rate
    DISTINCT_DAYS = train_distinct_days
    EFFECTIVE_N = train_eff_n
    SEALED_PRECISION = sealed_precision
    
    # Invariants check
    assert DISTINCT_DAYS <= ISSUED, f"DISTINCT_DAYS {DISTINCT_DAYS} > ISSUED {ISSUED}"
    assert EFFECTIVE_N < ISSUED, f"EFFECTIVE_N {EFFECTIVE_N} >= ISSUED {ISSUED}"
    
    print(f"ISSUED={ISSUED}")
    print(f"OPPORTUNITIES={OPPORTUNITIES}")
    print(f"PRECISION={PRECISION:.6f}")
    print(f"BASE_RATE={BASE_RATE:.6f}")
    print(f"DISTINCT_DAYS={DISTINCT_DAYS}")
    print(f"EFFECTIVE_N={EFFECTIVE_N:.6f}")
    print(f"SEALED_PRECISION={SEALED_PRECISION:.6f}")

if __name__ == '__main__':
    main()