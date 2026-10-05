# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 736
# cycle_index: 6
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

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all symbols with sufficient daily bars (tf='1d')
    cur.execute("""
        SELECT s.id, s.symbol, s.delisted_at
        FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
        AND EXISTS (
            SELECT 1 FROM bars b
            WHERE b.symbol_id = s.id AND b.tf = '1d'
            GROUP BY b.symbol_id
            HAVING COUNT(*) >= 252
        )
    """)
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = list(symbols.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # Get insider open-market purchases (code='P') with filed_ts
    cur.execute(f"""
        SELECT it.symbol_id, it.filed_ts, it.shares, it.price, it.accession
        FROM insider_trades it
        WHERE it.code = 'P'
        AND it.symbol_id IN ({placeholders})
        AND it.filed_ts IS NOT NULL
        ORDER BY it.filed_ts
    """, symbol_ids)
    insider_trades = cur.fetchall()
    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0

    # Get sentiment_features for hedged/n_polar ratio
    cur.execute(f"""
        SELECT symbol_id, day, hedged, n_polar
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        AND n_polar > 0
        ORDER BY symbol_id, day
    """, symbol_ids)
    sentiment_rows = cur.fetchall()

    # Build sentiment lookup: symbol_id -> {day: hedged_ratio}
    sentiment = {}
    for row in sentiment_rows:
        sid = row['symbol_id']
        day = row['day']
        ratio = row['hedged'] / row['n_polar'] if row['n_polar'] > 0 else 0
        if sid not in sentiment:
            sentiment[sid] = {}
        sentiment[sid][day] = ratio

    # Get daily bars for price and forward returns
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bar_rows = cur.fetchall()

    # Build bars lookup: symbol_id -> [(ts, close), ...]
    bars = {}
    for row in bar_rows:
        sid = row['symbol_id']
        if sid not in bars:
            bars[sid] = []
        bars[sid].append((row['ts'], row['close']))

    # For each symbol, compute historical hedged_ratio distribution for threshold
    hedged_thresholds = {}
    for sid, data in sentiment.items():
        ratios = sorted(data.values())
        if len(ratios) >= 20:
            idx = int(len(ratios) * 0.9)  # top decile
            hedged_thresholds[sid] = ratios[idx]
        else:
            hedged_thresholds[sid] = None

    # Process each insider trade as a decision point
    HORIZON_DAYS = 20
    LOOKBACK_DAYS = 252  # for hedged ratio history

    opportunities = 0
    issued = []
    sealed_cutoff = None

    # First pass: collect all valid decision points with their dates
    decision_points = []
    for trade in insider_trades:
        sid = trade['symbol_id']
        filed_ts = trade['filed_ts']
        filed_date = epoch_to_date(filed_ts)

        # Check symbol exists and not delisted
        if sid not in symbols:
            continue
        delisted_at = symbols[sid]['delisted_at']
        if delisted_at:
            delisted_date = str_to_date(delisted_at)
            if filed_date >= delisted_date:
                continue

        # Need sentiment data
        if sid not in sentiment or hedged_thresholds[sid] is None:
            continue

        # Need bars data
        if sid not in bars or len(bars[sid]) < LOOKBACK_DAYS + HORIZON_DAYS:
            continue

        # Find the trading day on or before filed_date
        bar_data = bars[sid]
        trade_bar_idx = None
        for i, (ts, close) in enumerate(bar_data):
            bar_date = epoch_to_date(ts)
            if bar_date <= filed_date:
                trade_bar_idx = i
            else:
                break

        if trade_bar_idx is None or trade_bar_idx < LOOKBACK_DAYS:
            continue

        # Need forward return data
        if trade_bar_idx + HORIZON_DAYS >= len(bar_data):
            continue

        # Compute 20-day avg hedged_ratio ending day before filed_date
        # sentiment_features day is calendar day
        prior_date = filed_date - timedelta(days=1)
        hedged_ratios = []
        for d in range(20):
            check_date = prior_date - timedelta(days=d)
            check_str = date_to_str(check_date)
            if check_str in sentiment[sid]:
                hedged_ratios.append(sentiment[sid][check_str])

        if len(hedged_ratios) < 10:  # require at least 10 days of data
            continue

        avg_hedged_ratio = sum(hedged_ratios) / len(hedged_ratios)
        threshold = hedged_thresholds[sid]

        if avg_hedged_ratio < threshold:
            continue  # not in top decile

        # Entry condition met
        entry_price = bar_data[trade_bar_idx][1]
        exit_price = bar_data[trade_bar_idx + HORIZON_DAYS][1]
        fwd_return = (exit_price - entry_price) / entry_price

        decision_points.append({
            'filed_ts': filed_ts,
            'filed_date': filed_date,
            'symbol_id': sid,
            'fwd_return': fwd_return,
            'entry_price': entry_price,
            'exit_price': exit_price,
            'avg_hedged_ratio': avg_hedged_ratio,
            'threshold': threshold
        })

    if not decision_points:
        print("INSUFFICIENT=1")
        return 0

    # Sort by filed_ts
    decision_points.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    n_total = len(decision_points)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_start_idx = n_total - n_sealed
    sealed_cutoff_ts = decision_points[sealed_start_idx]['filed_ts']

    # Compute base rate and precision
    # Predicted class: positive forward return (fwd_return > 0)
    all_issued = decision_points
    issued_count = len(all_issued)
    hits = sum(1 for dp in all_issued if dp['fwd_return'] > 0)
    precision = hits / issued_count if issued_count > 0 else 0
    base_rate = hits / issued_count if issued_count > 0 else 0  # base rate within issued subset

    # Distinct days among issued calls
    distinct_days = len(set(dp['filed_date'] for dp in all_issued))

    # Design effect (given as 6.99 in context)
    design_effect = 6.99
    effective_n = issued_count / design_effect

    # Sealed era precision
    sealed_points = [dp for dp in all_issued if dp['filed_ts'] >= sealed_cutoff_ts]
    sealed_issued = len(sealed_points)
    sealed_hits = sum(1 for dp in sealed_points if dp['fwd_return'] > 0)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

    # Opportunities = decision points considered (before filtering)
    # We need to count all decision points we evaluated, not just issued
    # But the spec says "count of decision points considered"
    # Let's count all insider trades that passed basic filters
    opportunities = len(decision_points)  # This is the issued count actually
    # Wait - opportunities should be all decision points we LOOKED AT
    # Let me recount: we looked at all insider trades, filtered step by step
    # The total considered is the number of insider trades for symbols in universe
    cur.execute(f"""
        SELECT COUNT(*) FROM insider_trades
        WHERE code = 'P' AND symbol_id IN ({placeholders}) AND filed_ts IS NOT NULL
    """, symbol_ids)
    opportunities = cur.fetchone()[0]

    # Print results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    # Check invariants
    if distinct_days > issued_count:
        print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
        sys.exit(1)
    if effective_n >= issued_count:
        print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)
        sys.exit(1)

    # Check observation floors
    if effective_n < 30 or distinct_days < 10:
        print("INSUFFICIENT=1")
        return 0

    return 0

if __name__ == '__main__':
    sys.exit(main())