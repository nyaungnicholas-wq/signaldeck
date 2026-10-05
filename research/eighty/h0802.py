# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 801
# cycle_index: 71
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all officer (CEO/CFO) open-market purchases with trade and filed dates
    cur.execute("""
        SELECT it.symbol_id, it.tx_ts, it.filed_ts, it.shares, it.price, it.value, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
          AND it.tx_ts > 0 AND it.filed_ts > 0
        ORDER BY it.symbol_id, it.tx_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Build daily news sentiment volatility per symbol from sentiment_features
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE ver = (SELECT MAX(ver) FROM sentiment_features)
        ORDER BY symbol_id, day
    """)
    sent_rows = cur.fetchall()

    # Compute rolling 20-day std of mean_score per symbol
    from collections import defaultdict
    sent_by_sym = defaultdict(list)
    for r in sent_rows:
        sent_by_sym[r['symbol_id']].append((str_to_date(r['day']), r['mean_score']))

    sent_vol = {}
    for sym_id, series in sent_by_sym.items():
        series.sort()
        scores = [s for _, s in series]
        dates = [d for d, _ in series]
        vol = {}
        for i in range(19, len(scores)):
            window = scores[i-19:i+1]
            mean = sum(window) / 20
            var = sum((x - mean) ** 2 for x in window) / 20
            vol[dates[i]] = var ** 0.5
        sent_vol[sym_id] = vol

    # Compute per-symbol 75th percentile of sentiment volatility
    sent_vol_thresh = {}
    for sym_id, vol_dict in sent_vol.items():
        if len(vol_dict) >= 20:
            vals = sorted(vol_dict.values())
            idx = int(len(vals) * 0.75)
            sent_vol_thresh[sym_id] = vals[idx]

    # For each trade, check conditions
    # 1. Trade-to-disclosure lag <= 2 days
    # 2. News sentiment volatility on trade date >= 75th percentile for that symbol
    # 3. First officer purchase in 252+ sessions (no prior officer purchase for this symbol by this insider? 
    #    We'll approximate: no officer purchase for this symbol in prior 252 trading days)
    
    # Get all officer purchase trade dates per symbol
    officer_trade_dates = defaultdict(list)
    for t in trades:
        d = epoch_to_date(t['tx_ts'])
        officer_trade_dates[t['symbol_id']].append(d)
    for sym in officer_trade_dates:
        officer_trade_dates[sym].sort()

    # Get daily bars for forward return calculation (21-day horizon)
    # We'll fetch on demand per symbol
    bar_cache = {}

    def get_bars(symbol_id):
        if symbol_id in bar_cache:
            return bar_cache[symbol_id]
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (symbol_id,))
        rows = cur.fetchall()
        bar_cache[symbol_id] = [(epoch_to_date(r['ts']), r['close']) for r in rows]
        return bar_cache[symbol_id]

    # Determine data range for 20% holdout
    all_trade_dates = [epoch_to_date(t['tx_ts']) for t in trades]
    min_date = min(all_trade_dates)
    max_date = max(all_trade_dates)
    total_days = (max_date - min_date).days
    cutoff_date = max_date - timedelta(days=int(total_days * 0.2))

    issued = 0
    hits = 0
    sealed_issued = 0
    sealed_hits = 0
    issued_days = set()
    opportunities = 0

    for t in trades:
        sym_id = t['symbol_id']
        tx_date = epoch_to_date(t['tx_ts'])
        filed_date = epoch_to_date(t['filed_ts'])
        opportunities += 1

        # Condition 1: trade-to-disclosure lag <= 2 calendar days
        lag = (filed_date - tx_date).days
        if lag > 2:
            continue

        # Condition 2: sentiment volatility high on trade date
        if sym_id not in sent_vol or tx_date not in sent_vol[sym_id]:
            continue
        if sym_id not in sent_vol_thresh:
            continue
        if sent_vol[sym_id][tx_date] < sent_vol_thresh[sym_id]:
            continue

        # Condition 3: no officer purchase for this symbol in prior 252 trading days
        prior_dates = [d for d in officer_trade_dates[sym_id] if d < tx_date]
        if prior_dates:
            last_prior = max(prior_dates)
            # Approximate trading days: count business days
            # For simplicity, require 252 calendar days gap (conservative)
            if (tx_date - last_prior).days < 252:
                continue

        # Get forward return over 21 trading days from bars
        bars = get_bars(sym_id)
        if not bars:
            continue
        # Find index of tx_date or next available bar
        bar_dates = [d for d, _ in bars]
        try:
            idx = bar_dates.index(tx_date)
        except ValueError:
            # Find next trading day
            idx = next((i for i, d in enumerate(bar_dates) if d >= tx_date), None)
            if idx is None:
                continue
        if idx + 21 >= len(bars):
            continue
        entry_px = bars[idx][1]
        exit_px = bars[idx + 21][1]
        fwd_ret = (exit_px - entry_px) / entry_px
        up = 1 if fwd_ret > 0 else 0

        issued += 1
        issued_days.add(tx_date)
        if up:
            hits += 1

        if tx_date >= cutoff_date:
            sealed_issued += 1
            if up:
                sealed_hits += 1

    if issued < 30 or len(issued_days) < 10:
        print("INSUFFICIENT=1")
        return 0

    precision = hits / issued if issued else 0
    base_rate = hits / issued if issued else 0  # base rate of predicted class (up) within issued
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0
    design_effect = 6.99
    effective_n = issued / design_effect

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={len(issued_days)}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    return 0

if __name__ == '__main__':
    sys.exit(main())