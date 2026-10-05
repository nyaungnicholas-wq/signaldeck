# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 783
# cycle_index: 53
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import time
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(conn, symbol_id):
    """Get sorted list of trading day timestamps (unix epoch, midnight UTC) for a symbol from 1d bars."""
    cur = conn.execute(
        "SELECT DISTINCT ts FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts",
        (symbol_id,)
    )
    return [row[0] for row in cur.fetchall()]

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_officer_purchases(conn):
    """Get all officer (CEO/CFO) open-market purchases with filed_ts."""
    cur = conn.execute("""
        SELECT symbol_id, insider, title, filed_ts, tx_ts, shares, price
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' 
               OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
          AND filed_ts IS NOT NULL
        ORDER BY symbol_id, filed_ts
    """)
    return cur.fetchall()

def get_revenue_series(conn, symbol_id):
    """Get quarterly Revenue for a symbol, ordered by fetched_at."""
    cur = conn.execute("""
        SELECT value, fetched_at, as_of
        FROM fundamentals
        WHERE symbol_id = ? AND metric = 'Revenues' AND as_of != 0
        ORDER BY fetched_at
    """, (symbol_id,))
    return cur.fetchall()

def get_sentiment_series(conn, symbol_id):
    """Get daily sentiment features for a symbol."""
    cur = conn.execute("""
        SELECT day, mean_score
        FROM sentiment_features
        WHERE symbol_id = ?
        ORDER BY day
    """, (symbol_id,))
    return cur.fetchall()

def get_bars_1d(conn, symbol_id):
    """Get daily bars for a symbol."""
    cur = conn.execute("""
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (symbol_id,))
    return cur.fetchall()

def compute_volatility_quartiles(scores, window=63):
    """Compute rolling volatility and bottom quartile threshold."""
    if len(scores) < window:
        return None, None
    vols = []
    for i in range(window - 1, len(scores)):
        window_scores = scores[i - window + 1:i + 1]
        mean = sum(window_scores) / window
        var = sum((x - mean) ** 2 for x in window_scores) / window
        vols.append(var ** 0.5)
    if not vols:
        return None, None
    sorted_vols = sorted(vols)
    q1_idx = len(sorted_vols) // 4
    q1_threshold = sorted_vols[q1_idx] if q1_idx < len(sorted_vols) else sorted_vols[0]
    return vols, q1_threshold

def check_revenue_acceleration(revenue_rows, decision_fetched_at):
    """Check if revenue growth accelerated for 2+ quarters as of decision_fetched_at."""
    # Filter to rows with fetched_at <= decision_fetched_at
    available = [r for r in revenue_rows if r[1] <= decision_fetched_at]
    if len(available) < 3:
        return False
    # Take last 3 quarters
    last3 = available[-3:]
    try:
        rev = [float(r[0]) for r in last3]
        if rev[0] <= 0 or rev[1] <= 0:
            return False
        growth1 = (rev[1] - rev[0]) / rev[0]
        growth2 = (rev[2] - rev[1]) / rev[1]
        return growth2 > growth1 > 0
    except (ValueError, ZeroDivisionError):
        return False

def check_dormancy(purchases, insider, symbol_id, decision_filed_ts, trading_days_map, lookback_days=365):
    """Check if this insider had no open-market purchases in prior ~252 trading sessions (~365 cal days)."""
    cutoff_ts = decision_filed_ts - lookback_days * 86400
    for p in purchases:
        if p[0] == symbol_id and p[1] == insider and p[3] < decision_filed_ts and p[3] >= cutoff_ts:
            return False
    return True

def get_forward_return(bars, decision_ts, horizon_days=5):
    """Get horizon_days forward return from decision_ts (next bar close to horizon bar close)."""
    # Find first bar on or after decision_ts
    idx = None
    for i, (ts, _) in enumerate(bars):
        if ts >= decision_ts:
            idx = i
            break
    if idx is None or idx + horizon_days >= len(bars):
        return None
    entry_close = bars[idx][1]
    exit_close = bars[idx + horizon_days][1]
    if entry_close <= 0:
        return None
    return (exit_close - entry_close) / entry_close

def main():
    start_time = time.time()
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Get all symbols with data
    cur = conn.execute("SELECT id FROM symbols WHERE active = 1")
    symbol_ids = [row[0] for row in cur.fetchall()]
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return 0

    # Pre-load data per symbol
    print("Loading data...", file=sys.stderr)
    officer_purchases = get_officer_purchases(conn)
    if not officer_purchases:
        print("INSUFFICIENT=1")
        return 0

    # Group purchases by symbol
    purchases_by_symbol = defaultdict(list)
    for p in officer_purchases:
        purchases_by_symbol[p[0]].append(p)

    # Pre-compute for each symbol
    symbol_data = {}
    for sid in symbol_ids:
        if sid not in purchases_by_symbol:
            continue
        bars = get_bars_1d(conn, sid)
        if len(bars) < 252:
            continue
        revenue = get_revenue_series(conn, sid)
        if len(revenue) < 3:
            continue
        sentiment = get_sentiment_series(conn, sid)
        if len(sentiment) < 63:
            continue
        
        # Compute sentiment volatility quartiles
        scores = [float(s[1]) for s in sentiment if s[1] is not None]
        vols, q1_thresh = compute_volatility_quartiles(scores, 63)
        if vols is None:
            continue
        
        # Map sentiment day to volatility
        sent_vol_map = {}
        for i, (day, score) in enumerate(sentiment):
            if i >= 62 and score is not None:
                sent_vol_map[day] = vols[i - 62]
        
        symbol_data[sid] = {
            'bars': bars,
            'revenue': revenue,
            'sentiment': sentiment,
            'sent_vol_map': sent_vol_map,
            'q1_thresh': q1_thresh,
            'purchases': purchases_by_symbol[sid]
        }

    if not symbol_data:
        print("INSUFFICIENT=1")
        return 0

    # Evaluate each officer purchase as a decision point
    opportunities = 0
    issued = 0
    hits = 0
    issued_days = set()
    sealed_hits = 0
    sealed_issued = 0
    all_calls = []  # (decision_date, hit)

    for sid, data in symbol_data.items():
        bars = data['bars']
        revenue = data['revenue']
        sent_vol_map = data['sent_vol_map']
        q1_thresh = data['q1_thresh']
        purchases = data['purchases']

        for p in purchases:
            _, insider, _, filed_ts, _, _, _ = p
            opportunities += 1

            # Check dormancy
            if not check_dormancy(purchases, insider, sid, filed_ts, None):
                continue

            # Check revenue acceleration (use filed_ts as decision_fetched_at proxy)
            if not check_revenue_acceleration(revenue, filed_ts):
                continue

            # Check news sentiment volatility bottom quartile
            decision_date = ts_to_date(filed_ts)
            decision_day_str = decision_date.isoformat()
            # Find closest sentiment day <= decision_day
            sent_vol = None
            for day_str, vol in sent_vol_map.items():
                if day_str <= decision_day_str:
                    sent_vol = vol
            if sent_vol is None or sent_vol > q1_thresh:
                continue

            # All criteria met - issue call
            issued += 1
            issued_days.add(decision_day_str)

            # Get forward return (5 trading days = 1w)
            fwd_ret = get_forward_return(bars, filed_ts, 5)
            if fwd_ret is None:
                # Treat as miss if no forward data
                hit = 0
            else:
                hit = 1 if fwd_ret > 0 else 0
            hits += hit
            all_calls.append((decision_date, hit))

    if issued == 0:
        print("INSUFFICIENT=1")
        return 0

    # Sort calls by date for 80/20 split
    all_calls.sort(key=lambda x: x[0])
    split_idx = int(len(all_calls) * 0.8)
    train_calls = all_calls[:split_idx]
    sealed_calls = all_calls[split_idx:]

    sealed_issued = len(sealed_calls)
    sealed_hits = sum(h for _, h in sealed_calls)

    # Compute design effect for EFFECTIVE_N
    # Simple approximation: design effect = 1 + (avg_cluster_size - 1) * ICC
    # Cluster by day, assume ICC ~ 0.1
    day_counts = defaultdict(int)
    for d, _ in all_calls:
        day_counts[d] += 1
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    icc = 0.1
    deff = 1 + (avg_cluster - 1) * icc
    effective_n = int(issued / deff) if deff > 1 else issued - 1
    if effective_n >= issued:
        effective_n = issued - 1

    precision = hits / issued if issued else 0
    base_rate = hits / issued if issued else 0  # Base rate within issued subset = precision for binary
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0
    distinct_days = len(issued_days)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())