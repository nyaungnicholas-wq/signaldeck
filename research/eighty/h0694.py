# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 693
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def utc_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    con = sqlite3.connect(DB_PATH, uri=True)
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Get all open-market insider purchases (code='P') with filed_ts
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
        ORDER BY it.filed_ts
    """)
    insider_buys = cur.fetchall()
    if not insider_buys:
        print("INSUFFICIENT=1")
        return

    symbol_ids = sorted({row['symbol_id'] for row in insider_buys})

    # 2. Fetch all daily bars (tf='1d') for these symbols
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_rows = cur.fetchall()

    # Organize bars by symbol_id: list of (ts, close)
    bars_by_sym = defaultdict(list)
    for row in bars_rows:
        bars_by_sym[row['symbol_id']].append((row['ts'], row['close']))

    # 3. For each insider buy, find decision bar index and compute signals
    opportunities = []
    issued = []
    labels = []
    decision_dates = []

    for row in insider_buys:
        sym_id = row['symbol_id']
        filed_ts = row['filed_ts']
        decision_date = utc_date(filed_ts)

        bars = bars_by_sym.get(sym_id, [])
        if len(bars) < 252 + 21:
            continue

        # Find the last bar with ts <= filed_ts (end of decision day)
        # bars ts are unix epochs; assume they represent daily close timestamps
        # We'll use the bar whose date <= decision_date
        bar_dates = [utc_date(ts) for ts, _ in bars]
        idx = -1
        for i, bd in enumerate(bar_dates):
            if bd <= decision_date:
                idx = i
            else:
                break
        if idx < 252 or idx + 21 >= len(bars):
            continue

        # Sufficient history: 252 prior bars (idx-251 .. idx), 21 forward bars (idx+1 .. idx+21)
        prior_closes = [bars[i][1] for i in range(idx - 251, idx + 1)]
        current_close = prior_closes[-1]
        low_252 = min(prior_closes)

        # 20-day volatility (std of daily returns)
        if len(prior_closes) < 21:
            continue
        rets_20 = [(prior_closes[i] - prior_closes[i-1]) / prior_closes[i-1] for i in range(-20, 0)]
        vol_20 = math.sqrt(sum(r*r for r in rets_20) / len(rets_20)) if rets_20 else 0

        # 252-day history of 20-day volatility (rolling)
        vol_history = []
        for i in range(252 - 20):
            window = prior_closes[i:i+21]
            if len(window) < 21:
                continue
            r = [(window[j] - window[j-1]) / window[j-1] for j in range(1, 21)]
            v = math.sqrt(sum(x*x for x in r) / len(r)) if r else 0
            vol_history.append(v)
        if len(vol_history) < 20:
            continue
        vol_history.sort()
        vol_25th = vol_history[len(vol_history) // 4]

        # Conditions
        near_low = current_close <= 1.05 * low_252
        low_vol = vol_20 <= vol_25th

        # Forward return over 21 trading days
        future_close = bars[idx + 21][1]
        fwd_ret = (future_close - current_close) / current_close
        label = 1 if fwd_ret > 0 else 0

        opportunities.append((decision_date, sym_id, label))
        if near_low and low_vol:
            issued.append((decision_date, sym_id, label))
            labels.append(label)
            decision_dates.append(decision_date)

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # 4. Split by date: most recent 20% of decision dates as sealed
    unique_dates = sorted({d for d, _, _ in opportunities})
    split_idx = int(len(unique_dates) * 0.8)
    if split_idx == 0 or split_idx == len(unique_dates):
        print("INSUFFICIENT=1")
        return
    train_dates = set(unique_dates[:split_idx])
    sealed_dates = set(unique_dates[split_idx:])

    # 5. Compute metrics
    issued_train = [l for d, _, l in issued if d in train_dates]
    issued_sealed = [l for d, _, l in issued if d in sealed_dates]

    if not issued_train:
        print("INSUFFICIENT=1")
        return

    issued_count = len(issued)
    opportunities_count = len(opportunities)
    precision = sum(labels) / issued_count if issued_count else 0.0
    base_rate = precision  # same for long-only
    distinct_days = len({d for d, _, _ in issued})

    # Effective N: cluster by UTC month (year-month)
    issued_months = len({(d.year, d.month) for d, _, _ in issued})
    effective_n = issued_months if issued_months > 0 else 1
    if effective_n >= issued_count:
        effective_n = issued_count - 1 if issued_count > 1 else 1

    sealed_precision = sum(issued_sealed) / len(issued_sealed) if issued_sealed else 0.0

    # 6. Output
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()