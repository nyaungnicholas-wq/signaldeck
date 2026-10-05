# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 656
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import bisect

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def business_days_between(start_date, end_date):
    """Count business days from start_date (exclusive) to end_date (inclusive)"""
    count = 0
    current = start_date + timedelta(days=1)
    while current <= end_date:
        if current.weekday() < 5:
            count += 1
        current += timedelta(days=1)
    return count

def add_business_days(start_date, n):
    """Add n business days to start_date"""
    current = start_date
    added = 0
    while added < n:
        current += timedelta(days=1)
        if current.weekday() < 5:
            added += 1
    return current

def percentile(values, p):
    """Compute p-th percentile (0-100) of sorted values"""
    if not values:
        return None
    idx = (p / 100) * (len(values) - 1)
    lo = int(idx)
    hi = min(lo + 1, len(values) - 1)
    if lo == hi:
        return values[lo]
    return values[lo] + (values[hi] - values[lo]) * (idx - lo)

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load all insider trades with code='P' (open market purchase)
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    all_trades = cur.fetchall()

    if not all_trades:
        print("INSUFFICIENT=1")
        return 0

    # Load symbols for market filtering
    cur.execute("SELECT id, symbol, market FROM symbols")
    symbols = {row['id']: row for row in cur.fetchall()}

    # Load daily bars for all symbols needed
    symbol_ids = set(row['symbol_id'] for row in all_trades)
    bars_by_symbol = defaultdict(list)
    for sid in symbol_ids:
        cur.execute("""
            SELECT ts, close, volume FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sid,))
        bars_by_symbol[sid] = [(row['ts'], row['close'], row['volume']) for row in cur.fetchall()]

    # Build insider purchase history per (symbol_id, insider)
    insider_history = defaultdict(list)  # (symbol_id, insider) -> list of (tx_ts, purchase_size)
    for trade in all_trades:
        key = (trade['symbol_id'], trade['insider'])
        purchase_size = trade['shares'] * trade['price']
        insider_history[key].append((trade['tx_ts'], purchase_size))

    # Sort each insider's history by tx_ts
    for key in insider_history:
        insider_history[key].sort(key=lambda x: x[0])

    # Filter for officer/director (exclude pure 10% owners)
    def is_officer_director(title):
        if not title:
            return False
        title_lower = title.lower()
        # Exclude if only 10% owner
        if '10%' in title_lower or 'ten percent' in title_lower:
            # Check if it has officer/director terms too
            officer_terms = ['officer', 'director', 'ceo', 'cfo', 'coo', 'cto', 'president', 'vice president', 'vp', 'chairman', 'secretary', 'treasurer', 'controller', 'principal']
            if not any(term in title_lower for term in officer_terms):
                return False
        return True

    # Process each trade as a potential signal
    signals = []  # (disclosure_date, symbol_id, insider, purchase_size, entry_price, forward_return, label)

    for trade in all_trades:
        symbol_id = trade['symbol_id']
        insider = trade['insider']
        title = trade['title']
        shares = trade['shares']
        price = trade['price']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']

        if not is_officer_director(title):
            continue

        # Disclosure delay check
        tx_date = epoch_to_date(tx_ts)
        filed_date = epoch_to_date(filed_ts)
        delay_bdays = business_days_between(tx_date, filed_date)
        if delay_bdays > 5:
            continue

        # Get bars for this symbol
        bars = bars_by_symbol.get(symbol_id, [])
        if len(bars) < 252:
            continue

        # Find disclosure day bar (filed_date)
        filed_epoch_start = date_to_epoch(filed_date)
        filed_epoch_end = filed_epoch_start + 86400
        disclosure_bar = None
        for ts, close, vol in bars:
            if filed_epoch_start <= ts < filed_epoch_end:
                disclosure_bar = (ts, close, vol)
                break
        if not disclosure_bar:
            continue
        _, disclosure_close, _ = disclosure_bar

        # Check close within 3% of trade price
        if abs(disclosure_close - price) / price > 0.03:
            continue

        # Compute 252-day avg daily dollar volume through T-1 (day before disclosure)
        # T-1 is the trading day before filed_date
        prev_bday = filed_date - timedelta(days=1)
        while prev_bday.weekday() >= 5:
            prev_bday -= timedelta(days=1)
        prev_bday_epoch = date_to_epoch(prev_bday)

        # Collect last 252 trading days ending at prev_bday
        dollar_volumes = []
        for ts, close, vol in bars:
            bar_date = epoch_to_date(ts)
            if bar_date > prev_bday:
                continue
            if bar_date.weekday() >= 5:
                continue
            dollar_volumes.append(close * vol)
            if len(dollar_volumes) >= 252:
                break
        if len(dollar_volumes) < 252:
            continue
        dollar_volumes.sort()
        avg_dollar_vol = sum(dollar_volumes) / 252
        if avg_dollar_vol < 1_000_000:
            continue
        p90_dollar_vol = percentile(dollar_volumes, 90)

        # Insider prior purchases
        key = (symbol_id, insider)
        history = insider_history[key]
        # Prior purchases before this trade's tx_ts
        prior_sizes = [sz for ts, sz in history if ts < tx_ts]
        if len(prior_sizes) < 10:
            continue
        prior_sizes.sort()
        p90_insider = percentile(prior_sizes, 90)

        purchase_size = shares * price
        if purchase_size < p90_dollar_vol:
            continue
        if purchase_size < p90_insider:
            continue

        # Compute 21-day forward return from disclosure close
        # Find disclosure bar index
        disc_idx = None
        for i, (ts, _, _) in enumerate(bars):
            if ts == disclosure_bar[0]:
                disc_idx = i
                break
        if disc_idx is None:
            continue

        # Need 21 trading days forward (only count tf='1d' bars on business days)
        forward_idx = disc_idx
        trading_days = 0
        for i in range(disc_idx + 1, len(bars)):
            ts, _, _ = bars[i]
            d = epoch_to_date(ts)
            if d.weekday() < 5:
                trading_days += 1
                if trading_days == 21:
                    forward_idx = i
                    break
        if trading_days < 21:
            continue

        forward_close = bars[forward_idx][1]
        forward_return = (forward_close - disclosure_close) / disclosure_close
        label = 1 if forward_return > 0 else 0

        signals.append({
            'filed_date': filed_date,
            'filed_ts': filed_ts,
            'symbol_id': symbol_id,
            'insider': insider,
            'label': label,
            'forward_return': forward_return
        })

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # Sort by disclosure date
    signals.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    n_total = len(signals)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed

    train_signals = signals[:n_train]
    sealed_signals = signals[n_train:]

    def compute_metrics(sig_list, label_name):
        if not sig_list:
            return None
        issued = len(sig_list)
        hits = sum(1 for s in sig_list if s['label'] == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate of positive class in issued subset
        distinct_days = len(set(s['filed_date'] for s in sig_list))
        
        # Design effect: cluster by day, compute effective N
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use Kish's effective sample size: n_eff = (sum w)^2 / sum(w^2) where w=1 per call
        # But clustered by day: if multiple calls same day, they're correlated
        day_counts = defaultdict(int)
        for s in sig_list:
            day_counts[s['filed_date']] += 1
        # Design effect approx = 1 + (cv^2) * (avg_cluster_size - 1) * ICC
        # Conservative: assume ICC=0.5, use actual clustering
        cluster_sizes = list(day_counts.values())
        if len(cluster_sizes) > 1:
            mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
            # Conservative ICC of 0.3 for same-day calls
            icc = 0.3
            deff = 1 + (mean_cluster - 1) * icc
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

    train_metrics = compute_metrics(train_signals, 'train')
    sealed_metrics = compute_metrics(sealed_signals, 'sealed')

    # Overall metrics (for reporting)
    all_metrics = compute_metrics(signals, 'all')

    # Opportunities = decision points considered = number of qualifying insider trades we evaluated
    # This is the number of officer/director code P trades that passed basic filters (before our thresholds)
    # Actually, the spec says "OPPORTUNITIES=<count of decision points considered>"
    # Decision points = each insider trade that we could have made a call on (had data)
    # But we need to count all officer/director P trades with sufficient data to evaluate
    # Let's count: all officer/director P trades where we had 252 bars, 10 prior, etc.
    # Actually, simpler: opportunities = number of insider trades that passed the abstain checks
    # (i.e., had all required data available) regardless of whether they met entry thresholds
    
    # Re-count opportunities properly
    opportunities = 0
    for trade in all_trades:
        if not is_officer_director(trade['title']):
            continue
        tx_date = epoch_to_date(trade['tx_ts'])
        filed_date = epoch_to_date(trade['filed_ts'])
        if business_days_between(tx_date, filed_date) > 5:
            continue
        symbol_id = trade['symbol_id']
        bars = bars_by_symbol.get(symbol_id, [])
        if len(bars) < 252:
            continue
        # Check disclosure bar exists
        filed_epoch_start = date_to_epoch(filed_date)
        filed_epoch_end = filed_epoch_start + 86400
        has_disclosure_bar = any(filed_epoch_start <= ts < filed_epoch_end for ts, _, _ in bars)
        if not has_disclosure_bar:
            continue
        # Check 252-day volume history
        prev_bday = filed_date - timedelta(days=1)
        while prev_bday.weekday() >= 5:
            prev_bday -= timedelta(days=1)
        dollar_volumes = []
        for ts, close, vol in bars:
            bar_date = epoch_to_date(ts)
            if bar_date > prev_bday or bar_date.weekday() >= 5:
                continue
            dollar_volumes.append(close * vol)
            if len(dollar_volumes) >= 252:
                break
        if len(dollar_volumes) < 252:
            continue
        # Check insider history
        key = (symbol_id, trade['insider'])
        history = insider_history[key]
        prior_count = sum(1 for ts, _ in history if ts < trade['tx_ts'])
        if prior_count < 10:
            continue
        # Check forward 21 days exist
        # Find disclosure bar index
        disc_idx = None
        for i, (ts, _, _) in enumerate(bars):
            if filed_epoch_start <= ts < filed_epoch_end:
                disc_idx = i
                break
        if disc_idx is None:
            continue
        trading_days = 0
        for i in range(disc_idx + 1, len(bars)):
            d = epoch_to_date(bars[i][0])
            if d.weekday() < 5:
                trading_days += 1
                if trading_days == 21:
                    break
        if trading_days < 21:
            continue
        opportunities += 1

    # Print required output
    print(f"ISSUED={all_metrics['issued']}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_metrics['precision']:.6f}")
    print(f"BASE_RATE={all_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={all_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={all_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}" if sealed_metrics else "SEALED_PRECISION=0.000000")

    return 0

if __name__ == '__main__':
    sys.exit(main())