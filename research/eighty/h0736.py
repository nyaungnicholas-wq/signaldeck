# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 735
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load all insider trades, sorted by symbol, insider, trade time
    cur.execute("""
        SELECT symbol_id, insider, code, shares, tx_ts, filed_ts
        FROM insider_trades
        ORDER BY symbol_id, insider, tx_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # 2. For each insider-symbol, track purchases with holding period since last sale
    events = []  # (symbol_id, filed_ts, tx_ts)
    from collections import defaultdict
    last_sale = defaultdict(lambda: 0)  # (symbol_id, insider) -> tx_ts of last sale
    first_purchase = defaultdict(lambda: 0)  # (symbol_id, insider) -> tx_ts of first purchase

    for row in trades:
        key = (row['symbol_id'], row['insider'])
        code = row['code']
        tx_ts = row['tx_ts']
        filed_ts = row['filed_ts']

        if code == 'P':  # open-market purchase
            # holding period = time since last sale, or since first purchase if no sale
            if last_sale[key] > 0:
                hold_days = (tx_ts - last_sale[key]) / 86400
            elif first_purchase[key] > 0:
                hold_days = (tx_ts - first_purchase[key]) / 86400
            else:
                hold_days = 0
                first_purchase[key] = tx_ts

            if hold_days >= 1095:  # 3 years
                events.append((row['symbol_id'], filed_ts, tx_ts))
        elif code in ('S', 'F'):  # sale or tax sale
            last_sale[key] = tx_ts
        elif code in ('A', 'M'):  # award or option exercise - treat as purchase for holding period start
            if first_purchase[key] == 0:
                first_purchase[key] = tx_ts

    if not events:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get 21-day forward returns from daily bars for each event
    # Bars: symbol_id, tf, ts, close. tf='1d', ts is unix epoch (start of day UTC)
    # For each event, find the bar on or after filed_ts (decision date), then 21 bars later
    symbol_ids = list(set(e[0] for e in events))
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars = cur.fetchall()

    # Organize bars by symbol_id
    bars_by_symbol = defaultdict(list)
    for row in bars:
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    # For each event, compute 21-day forward return
    results = []  # (decision_date, symbol_id, fwd_return)
    for symbol_id, filed_ts, tx_ts in events:
        b = bars_by_symbol.get(symbol_id, [])
        if not b:
            continue
        # Find index of first bar with ts >= filed_ts (decision date)
        idx = 0
        while idx < len(b) and b[idx][0] < filed_ts:
            idx += 1
        if idx >= len(b):
            continue
        entry_price = b[idx][1]
        # 21 trading days later
        if idx + 21 >= len(b):
            continue
        exit_price = b[idx + 21][1]
        fwd_return = (exit_price - entry_price) / entry_price
        # decision date as UTC date string
        decision_date = datetime.utcfromtimestamp(filed_ts).date()
        results.append((decision_date, symbol_id, fwd_return))

    if not results:
        print("INSUFFICIENT=1")
        return 0

    # 4. Aggregate to one observation per (symbol, decision_date)
    # If multiple insiders trigger same symbol-day, average their returns (or take max?).
    # Use average for conservatism.
    from collections import defaultdict
    obs_dict = defaultdict(list)
    for d, sym, ret in results:
        obs_dict[(d, sym)].append(ret)

    observations = []
    for (d, sym), rets in obs_dict.items():
        avg_ret = sum(rets) / len(rets)
        observations.append((d, sym, avg_ret))

    # Sort by date
    observations.sort(key=lambda x: x[0])

    # 5. Split: hold out most recent 20% as sealed era
    n = len(observations)
    split_idx = int(n * 0.8)
    train_obs = observations[:split_idx]
    sealed_obs = observations[split_idx:]

    def compute_metrics(obs_list):
        if not obs_list:
            return 0, 0, 0, 0, 0
        issued = len(obs_list)
        hits = sum(1 for _, _, ret in obs_list if ret > 0)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # base rate of positive class within issued
        distinct_days = len(set(d for d, _, _ in obs_list))
        # Effective N: conservative estimate = distinct_days (assumes perfect within-day correlation)
        effective_n = distinct_days if distinct_days < issued else issued - 1
        if effective_n >= issued:
            effective_n = issued - 1
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days, train_effective_n = compute_metrics(train_obs)
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct_days, sealed_effective_n = compute_metrics(sealed_obs)

    # 6. Print required metrics (using full sample for main metrics, sealed for SEALED_PRECISION)
    all_issued, all_hits, all_precision, all_base_rate, all_distinct_days, all_effective_n = compute_metrics(observations)

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={all_issued}")  # opportunities = decision points considered = issued (we only consider days with signals)
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())