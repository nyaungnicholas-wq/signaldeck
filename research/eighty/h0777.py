# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 776
# cycle_index: 46
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # MECHANISM: Insiders accumulate during quiet pullbacks in long-term uptrends when price forms 5+ consecutive lower highs/lows but 200-day SMA still rises, signaling conviction that the dip is transient.
    # HORIZON: 21 trading days
    # UNIVERSE: Symbols with market='stocks', active=1, >=252 daily bars (tf='1d') prior to decision, and at least one insider open-market purchase (code='P') in history.
    # ENTRY: Insider open-market purchase (code='P') filed on day T where: (a) prior 5+ daily bars show consecutive lower highs and lower lows, (b) 200-day SMA slope > 0 (SMA_T > SMA_T-21), (c) day T volume in bottom quartile of prior 63 sessions, (d) 21-day avg dollar volume >= $1M.
    # ABSTAIN: No call if any entry condition fails, if filing insider sold (code='S') in prior 63 sessions, if symbol has <3 qualifying purchases in full history, or if decision in sealed era (most recent 20% of sample).
    # CLAIM: Precision >= 0.58 on issued calls at 21-day horizon with issued-subset base rate <= 0.53 and abstention rate >= 0.90.

    # Get eligible symbols
    cur.execute("SELECT id, symbol FROM symbols WHERE market='stocks' AND active=1")
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return

    symbol_ids = list(symbols.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # Get insider purchases for eligible symbols
    cur.execute(f"""
        SELECT symbol_id, filed_ts, insider
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({placeholders})
        ORDER BY filed_ts
    """, symbol_ids)
    insider_purchases = cur.fetchall()
    if not insider_purchases:
        print("INSUFFICIENT=1")
        return

    # Get all daily bars for eligible symbols
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    all_bars = cur.fetchall()

    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for row in all_bars:
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume']
        })

    # Get insider sales for dormancy check
    cur.execute(f"""
        SELECT symbol_id, filed_ts, insider
        FROM insider_trades
        WHERE code='S' AND symbol_id IN ({placeholders})
        ORDER BY filed_ts
    """, symbol_ids)
    insider_sales = cur.fetchall()
    sales_by_symbol_insider = defaultdict(list)
    for row in insider_sales:
        sales_by_symbol_insider[(row['symbol_id'], row['insider'])].append(row['filed_ts'])

    # Process each insider purchase as a decision point
    decisions = []
    for row in insider_purchases:
        sym_id = row['symbol_id']
        filed_ts = row['filed_ts']
        insider = row['insider']

        bars = bars_by_symbol.get(sym_id, [])
        if len(bars) < 252:
            continue

        # Find decision bar index (last bar with ts <= filed_ts)
        # bars are sorted by ts ascending
        decision_idx = -1
        for i, bar in enumerate(bars):
            if bar['ts'] <= filed_ts:
                decision_idx = i
            else:
                break
        if decision_idx < 251:  # need 252 bars prior
            continue

        # Check insider selling dormancy: no sales by this insider in prior 63 sessions
        # Approximate 63 sessions ~ 63 trading days ~ 89 calendar days
        dormancy_cutoff = filed_ts - 89 * 86400
        recent_sales = [ts for ts in sales_by_symbol_insider.get((sym_id, insider), []) if ts > dormancy_cutoff]
        if recent_sales:
            continue

        # Get window of 252 bars ending at decision_idx
        window = bars[decision_idx - 251:decision_idx + 1]
        decision_bar = window[-1]

        # Condition (a): 5+ consecutive lower highs and lower lows ending at decision_bar
        # Check last 5 bars (indices -5 to -1)
        lower_highs_lows = True
        for i in range(-5, -1):
            if not (window[i]['high'] < window[i-1]['high'] and window[i]['low'] < window[i-1]['low']):
                lower_highs_lows = False
                break
        if not lower_highs_lows:
            continue

        # Condition (b): 200-day SMA slope > 0 (SMA_T > SMA_T-21)
        # SMA over last 200 bars vs SMA over 200 bars ending 21 days ago
        if len(window) < 221:
            continue
        sma_now = sum(b['close'] for b in window[-200:]) / 200
        sma_21_ago = sum(b['close'] for b in window[-221:-21]) / 200
        if sma_now <= sma_21_ago:
            continue

        # Condition (c): day T volume in bottom quartile of prior 63 sessions
        vol_window = [b['volume'] for b in window[-63:]]
        decision_vol = vol_window[-1]
        sorted_vols = sorted(vol_window)
        quartile_idx = len(sorted_vols) // 4
        if decision_vol > sorted_vols[quartile_idx]:
            continue

        # Condition (d): 21-day avg dollar volume >= $1M
        dollar_vols = [b['close'] * b['volume'] for b in window[-21:]]
        avg_dollar_vol = sum(dollar_vols) / 21
        if avg_dollar_vol < 1_000_000:
            continue

        # Compute 21-day forward return
        # Need 21 trading days after decision_idx
        if decision_idx + 21 >= len(bars):
            continue
        forward_bar = bars[decision_idx + 21]
        fwd_return = (forward_bar['close'] - decision_bar['close']) / decision_bar['close']
        label = 1 if fwd_return > 0 else 0

        # Decision date for sealed era split
        decision_date = datetime.utcfromtimestamp(filed_ts).date()

        decisions.append({
            'symbol_id': sym_id,
            'symbol': symbols[sym_id],
            'filed_ts': filed_ts,
            'decision_date': decision_date,
            'decision_idx': decision_idx,
            'label': label,
            'fwd_return': fwd_return
        })

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # Sort by decision date
    decisions.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(decisions) * 0.8)
    train_decisions = decisions[:split_idx]
    sealed_decisions = decisions[split_idx:]

    # Count qualifying events per symbol for history filter (ABSTAIN: <3 qualifying in full history)
    symbol_counts = defaultdict(int)
    for d in decisions:
        symbol_counts[d['symbol_id']] += 1

    # Apply history filter and deduplicate: one call per symbol per day
    def filter_and_dedupe(dec_list):
        issued = []
        seen = set()
        for d in dec_list:
            if symbol_counts[d['symbol_id']] < 3:
                continue
            key = (d['symbol_id'], d['decision_date'])
            if key in seen:
                continue
            seen.add(key)
            issued.append(d)
        return issued

    train_issued = filter_and_dedupe(train_decisions)
    sealed_issued = filter_and_dedupe(sealed_decisions)

    # Compute metrics for train (non-sealed)
    issued_count = len(train_issued)
    opportunities = len(train_decisions)  # all decision points considered before dedupe/history filter
    if issued_count == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(1 for d in train_issued if d['label'] == 1)
    precision = hits / issued_count
    base_rate = hits / issued_count  # base rate of predicted class (up) within issued subset

    distinct_days = len(set(d['decision_date'] for d in train_issued))

    # Design effect: measure clustering by time
    # Group by decision_date, count calls per day
    day_counts = defaultdict(int)
    for d in train_issued:
        day_counts[d['decision_date']] += 1
    # Design effect = 1 + (avg_cluster_size - 1) * intraclass_correlation
    # Simplified: variance inflation from clustering
    n_days = len(day_counts)
    if n_days > 1:
        avg_cluster = issued_count / n_days
        # Intraclass correlation approximation
        icc = max(0, (avg_cluster - 1) / (issued_count - 1)) if issued_count > 1 else 0
        design_effect = 1 + (avg_cluster - 1) * icc
    else:
        design_effect = issued_count
    effective_n = issued_count / design_effect if design_effect > 0 else 0

    # Sealed precision
    sealed_hits = sum(1 for d in sealed_issued if d['label'] == 1)
    sealed_issued_count = len(sealed_issued)
    sealed_precision = sealed_hits / sealed_issued_count if sealed_issued_count > 0 else 0.0

    # Print required lines
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()