# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 708
# cycle_index: 35
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
# MECHANISM: 10% owners (title contains '10%') make open-market purchases (code='P') exceeding $500k when the stock's 20-day realized volatility is in the bottom quartile of its 252-day range, signaling conviction during complacency.
# HORIZON: 21d
# UNIVERSE: Symbols with >=252 daily bars and at least one qualifying insider trade in the test window.
# ENTRY: filed_ts of insider trade where code='P', title LIKE '%10%%', shares*price > 500000, and 20-day vol (using bars up to filed_ts) <= 25th percentile of prior 252-day vol distribution.
# ABSTAIN: No qualifying trade on a given (symbol, day); or insufficient bars for vol/forward return; or trade filed_ts in sealed era.
# CLAIM: Precision > base rate + 0.10 on 21-day positive forward return.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all daily bars for symbols with sufficient history
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_rows = cur.fetchall()

    # Organize bars by symbol
    bars_by_symbol = {}
    for row in bars_rows:
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append((row['ts'], row['close']))

    # Filter symbols with >=252 bars
    valid_symbols = {sid for sid, bars in bars_by_symbol.items() if len(bars) >= 252}
    print(f"Symbols with >=252 daily bars: {len(valid_symbols)}", file=sys.stderr)

    # Get insider trades: code='P', title contains '10%', value > 500k
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, shares, price, shares * price as value, title
        FROM insider_trades
        WHERE code = 'P'
          AND title LIKE '%10%%'
          AND shares * price > 500000
        ORDER BY filed_ts
    """)
    trades = cur.fetchall()
    print(f"Raw qualifying trades: {len(trades)}", file=sys.stderr)

    # For each trade, compute 20-day vol at filed_ts and check bottom quartile
    # Also compute 21-day forward return from bars
    opportunities = []
    issued = []

    for trade in trades:
        sid = trade['symbol_id']
        if sid not in valid_symbols:
            continue
        filed_ts = trade['filed_ts']
        filed_date = epoch_to_date(filed_ts)

        bars = bars_by_symbol[sid]
        # Find index of last bar with ts <= filed_ts
        idx = -1
        for i, (ts, _) in enumerate(bars):
            if ts <= filed_ts:
                idx = i
            else:
                break
        if idx < 251:  # Need 252 bars for vol range
            continue
        if idx + 21 >= len(bars):  # Need 21 days forward
            continue

        # Compute daily returns for vol calculation
        closes = [c for _, c in bars[:idx+1]]
        rets = [(closes[i] - closes[i-1]) / closes[i-1] for i in range(1, len(closes))]

        # 20-day realized vol (std of last 20 returns)
        if len(rets) < 20:
            continue
        recent_rets = rets[-20:]
        import math
        mean_ret = sum(recent_rets) / len(recent_rets)
        vol_20 = math.sqrt(sum((r - mean_ret)**2 for r in recent_rets) / len(recent_rets))

        # 252-day vol distribution (rolling 20-day vols)
        vol_series = []
        for i in range(20, min(252, len(rets)) + 1):
            window = rets[i-20:i]
            m = sum(window) / len(window)
            v = math.sqrt(sum((r - m)**2 for r in window) / len(window))
            vol_series.append(v)
        if len(vol_series) < 20:
            continue
        vol_series.sort()
        p25 = vol_series[len(vol_series) // 4]

        if vol_20 > p25:
            continue

        # 21-day forward return
        entry_close = bars[idx][1]
        exit_close = bars[idx + 21][1]
        fwd_ret = (exit_close - entry_close) / entry_close
        hit = 1 if fwd_ret > 0 else 0

        opportunities.append((filed_ts, sid, hit, fwd_ret))
        issued.append((filed_ts, sid, hit, fwd_ret))

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Sort by filed_ts
    opportunities.sort(key=lambda x: x[0])
    issued.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(opportunities) * 0.8)
    train_ops = opportunities[:split_idx]
    sealed_ops = opportunities[split_idx:]

    train_issued = [o for o in issued if o[0] <= opportunities[split_idx-1][0]] if split_idx > 0 else []
    sealed_issued = [o for o in issued if o[0] > opportunities[split_idx-1][0]] if split_idx > 0 else issued

    def compute_metrics(ops):
        if not ops:
            return 0, 0, 0, 0, 0
        n = len(ops)
        hits = sum(o[2] for o in ops)
        precision = hits / n
        base_rate = hits / n  # Within issued subset, base rate of positive class
        distinct_days = len(set(epoch_to_date(o[0]) for o in ops))
        # Design effect: approximate as 1 + (avg cluster size - 1) * autocorr
        # Simple approximation: group by day, compute variance inflation
        day_counts = {}
        for o in ops:
            d = epoch_to_date(o[0])
            day_counts[d] = day_counts.get(d, 0) + 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        deff = max(1.0, avg_cluster)  # Conservative
        effective_n = n / deff
        return n, precision, base_rate, distinct_days, effective_n

    train_n, train_prec, train_br, train_days, train_eff = compute_metrics(train_issued)
    sealed_n, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_metrics(sealed_issued)

    print(f"ISSUED={len(issued)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={train_prec:.4f}")
    print(f"BASE_RATE={train_br:.4f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.4f}")

if __name__ == '__main__':
    main()