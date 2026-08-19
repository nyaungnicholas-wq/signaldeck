# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 786
# cycle_index: 56
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    keywords = ['CEO', 'CFO', 'PRESIDENT', 'CHIEF', 'OFFICER', 'COO', 'CTO', 'CIO', 'CMO', 'GC', 'GENERAL COUNSEL', 'TREASURER', 'CONTROLLER', 'PRINCIPAL ACCOUNTING', 'PRINCIPAL FINANCIAL', 'PRINCIPAL EXECUTIVE', 'VICE PRESIDENT', 'VP ', 'V.P.', 'EXECUTIVE VP', 'EVP', 'SVP', 'SENIOR VP']
    return any(k in t for k in keywords)

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all officer open-market purchases (code='P')
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code, it.shares, it.price, it.value, it.tx_ts, it.filed_ts
        FROM insider_trades it
        WHERE it.code = 'P' AND it.value > 0
        ORDER BY it.tx_ts
    """)
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Filter to officers
    officer_trades = [t for t in trades if is_officer(t['title'])]
    if not officer_trades:
        print("INSUFFICIENT=1")
        return 0

    # Get symbols with bars coverage (tf='1d')
    cur.execute("""
        SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'
    """)
    bar_symbols = {row['symbol_id'] for row in cur.fetchall()}

    # Filter trades to symbols with daily bars
    officer_trades = [t for t in officer_trades if t['symbol_id'] in bar_symbols]
    if not officer_trades:
        print("INSUFFICIENT=1")
        return 0

    # Build sentiment history per symbol
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE mean_score IS NOT NULL
        ORDER BY symbol_id, day
    """)
    sent_rows = cur.fetchall()

    sentiment_by_symbol = {}
    for row in sent_rows:
        sid = row['symbol_id']
        if sid not in sentiment_by_symbol:
            sentiment_by_symbol[sid] = []
        sentiment_by_symbol[sid].append((row['day'], row['mean_score']))

    # Precompute 20-day rolling average and 252-day historical deciles for each symbol
    # We'll compute on the fly per trade for correctness (as-of discipline)

    # Build officer purchase history for personal conviction
    officer_history = {}
    for t in officer_trades:
        key = (t['symbol_id'], t['insider'])
        if key not in officer_history:
            officer_history[key] = []
        officer_history[key].append((t['tx_ts'], t['value']))

    # Sort each officer's history by tx_ts
    for key in officer_history:
        officer_history[key].sort(key=lambda x: x[0])

    # Get daily bars for forward returns
    # We'll query bars as needed per trade

    results = []  # (tx_ts, symbol_id, hit, trade_date_str)

    for t in officer_trades:
        tx_ts = t['tx_ts']
        symbol_id = t['symbol_id']
        insider = t['insider']
        trade_value = t['value']
        trade_date = epoch_to_date(tx_ts)
        trade_date_str = date_to_str(trade_date)

        # 1. Check sentiment condition: 20-day avg sentiment up to trade_date in bottom decile of 252-day history
        if symbol_id not in sentiment_by_symbol:
            continue
        sent_data = sentiment_by_symbol[symbol_id]
        # Filter to days <= trade_date
        hist = [(d, s) for d, s in sent_data if d <= trade_date_str]
        if len(hist) < 252:
            continue
        # Compute 20-day rolling averages for the last 252 days
        rolling_20 = []
        for i in range(19, len(hist)):
            window = hist[i-19:i+1]
            avg = sum(s for _, s in window) / 20.0
            rolling_20.append((hist[i][0], avg))
        if len(rolling_20) < 252:
            continue
        # The most recent 20-day avg (ending on trade_date or latest available <= trade_date)
        current_20d_avg = rolling_20[-1][1]
        # Historical 20-day avgs (excluding the most recent one to avoid lookahead? 
        # As-of discipline: at trade_date, we know up to trade_date. The current 20-day avg uses data up to trade_date.
        # The historical distribution should be from prior 252 trading days.
        # Use the 252 values ending at trade_date (including current). But for decile, we want to know where current stands relative to history.
        # Use the last 252 rolling_20 values (which end at trade_date).
        hist_20d = [v for _, v in rolling_20[-252:]]
        if len(hist_20d) < 252:
            continue
        # Compute decile threshold (bottom 10%)
        sorted_hist = sorted(hist_20d)
        decile_10 = sorted_hist[int(0.1 * 252)]
        if current_20d_avg > decile_10:
            continue  # not in bottom decile

        # 2. Check personal conviction: trade_value in top quartile of officer's prior 5-year purchases
        key = (symbol_id, insider)
        hist_trades = officer_history.get(key, [])
        # Prior trades strictly before this tx_ts
        prior_values = [v for ts, v in hist_trades if ts < tx_ts]
        # Limit to last 5 years (approx 1825 days)
        five_years_ago = tx_ts - 5 * 365 * 24 * 3600
        prior_values = [v for ts, v in hist_trades if ts < tx_ts and ts >= five_years_ago]
        if len(prior_values) < 4:  # need at least 4 to have a quartile
            continue
        sorted_prior = sorted(prior_values)
        q75 = sorted_prior[int(0.75 * len(sorted_prior))]
        if trade_value <= q75:
            continue

        # 3. Compute 21-trading-day forward return from daily bars
        # Get close on trade_date (or next available) and close 21 trading days later
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts LIMIT 50
        """, (symbol_id, tx_ts))
        bars_after = cur.fetchall()
        if len(bars_after) < 22:
            continue
        entry_close = bars_after[0]['close']
        exit_close = bars_after[21]['close']  # 21 trading days later (0-indexed)
        if entry_close <= 0 or exit_close <= 0:
            continue
        fwd_return = (exit_close - entry_close) / entry_close
        hit = 1 if fwd_return > 0 else 0

        results.append((tx_ts, symbol_id, hit, trade_date_str))

    if not results:
        print("INSUFFICIENT=1")
        return 0

    # Deduplicate by (symbol_id, trade_date_str) - one observation per symbol-day
    seen = set()
    deduped = []
    for tx_ts, sid, hit, dstr in results:
        key = (sid, dstr)
        if key not in seen:
            seen.add(key)
            deduped.append((tx_ts, sid, hit, dstr))

    # Sort by time
    deduped.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    n_total = len(deduped)
    n_sealed = max(1, int(math.ceil(n_total * 0.2)))
    train = deduped[:-n_sealed]
    sealed = deduped[-n_sealed:]

    def compute_metrics(data):
        if not data:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(data)
        hits = sum(h for _, _, h, _ in data)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # base rate of predicted class (positive return) within issued subset
        distinct_days = len(set(d for _, _, _, d in data))
        # Design effect: cluster by symbol
        from collections import defaultdict
        clusters = defaultdict(list)
        for _, sid, hit, _ in data:
            clusters[sid].append(hit)
        k = len(clusters)
        N = issued
        if k <= 1:
            design_effect = 1.0
        else:
            p = hits / N
            # Between-cluster variance
            between_sum = 0.0
            within_sum = 0.0
            for sid, hits_list in clusters.items():
                n_i = len(hits_list)
                p_i = sum(hits_list) / n_i
                between_sum += n_i * (p_i - p) ** 2
                within_sum += n_i * p_i * (1 - p_i)
            between_var = between_sum / (k - 1) if k > 1 else 0
            within_var = within_sum / (N - k) if N > k else 0
            avg_n = N / k
            if between_var + (avg_n - 1) * within_var > 0:
                icc = (between_var - within_var) / (between_var + (avg_n - 1) * within_var)
                icc = max(0.0, min(1.0, icc))
            else:
                icc = 0.0
            design_effect = 1 + (avg_n - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_train, hits_train, prec_train, base_train, days_train, eff_train = compute_metrics(train)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, days_sealed, eff_sealed = compute_metrics(sealed)

    # Overall metrics (for reporting)
    issued_all, hits_all, prec_all, base_all, days_all, eff_all = compute_metrics(deduped)

    # Print required lines
    print(f"ISSUED={issued_all}")
    print(f"OPPORTUNITIES={len(officer_trades)}")  # decision points considered (officer trades)
    print(f"PRECISION={prec_all:.6f}")
    print(f"BASE_RATE={base_all:.6f}")
    print(f"DISTINCT_DAYS={days_all}")
    print(f"EFFECTIVE_N={eff_all:.6f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())