# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 867
# cycle_index: 13
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

    # Get officer (CEO/CFO) open-market purchases
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, it.tx_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' 
               OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
          AND s.active = 1
        ORDER BY it.filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # Group trades by symbol
    trades_by_symbol = defaultdict(list)
    for t in trades:
        trades_by_symbol[t['symbol_id']].append(t)

    symbol_ids = list(trades_by_symbol.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # Fetch daily bars for these symbols
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_rows = cur.fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'], 'open': row['open'], 'close': row['close']
        })

    # Fetch news dates for these symbols
    cur.execute(f"""
        SELECT symbol_id, ts
        FROM news
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    news_rows = cur.fetchall()

    news_by_symbol = defaultdict(list)
    for row in news_rows:
        news_by_symbol[row['symbol_id']].append(row['ts'])

    calls = []  # (decision_ts, symbol_id, fwd_return, hit)

    for symbol_id, tlist in trades_by_symbol.items():
        bars = bars_by_symbol.get(symbol_id, [])
        if len(bars) < 252 + 21 + 5:
            continue

        # Precompute daily returns and rolling 21-day vol
        closes = [b['close'] for b in bars]
        rets = []
        for i in range(1, len(closes)):
            if closes[i-1] > 0:
                rets.append(closes[i] / closes[i-1] - 1)
            else:
                rets.append(0.0)

        # Rolling 21-day vol (std of returns)
        vol21 = []
        for i in range(20, len(rets)):
            window = rets[i-20:i+1]
            mean = sum(window) / len(window)
            var = sum((x - mean) ** 2 for x in window) / len(window)
            vol21.append(var ** 0.5)
        # vol21[i] corresponds to bars index i+20 (0-based)

        # For each bar, compute percentile of current vol21 in past 252 vol21 values
        vol_percentile = [0.0] * len(bars)
        for i in range(252 + 20, len(vol21)):
            current = vol21[i]
            past = vol21[i-252:i]
            if not past:
                continue
            rank = sum(1 for v in past if v <= current)
            vol_percentile[i + 20] = rank / len(past)

        # News timestamps set for fast lookup
        news_ts = set(news_by_symbol.get(symbol_id, []))

        for trade in tlist:
            filed_ts = trade['filed_ts']
            # Find bar index at or before filed_ts (last known bar at decision time)
            bar_idx = -1
            for i, b in enumerate(bars):
                if b['ts'] <= filed_ts:
                    bar_idx = i
                else:
                    break
            if bar_idx < 252 + 20:
                continue
            if bar_idx + 1 + 21 >= len(bars):
                continue

            # Check vol condition: bottom quintile (percentile <= 0.2)
            if vol_percentile[bar_idx] > 0.2:
                continue

            # Check zero news in prior 10 trading days
            # Need to check 10 trading days before bar_idx
            news_count = 0
            for i in range(max(0, bar_idx - 10), bar_idx + 1):
                if bars[i]['ts'] in news_ts:
                    news_count += 1
            if news_count > 0:
                continue

            # Entry: next day open
            entry_open = bars[bar_idx + 1]['open']
            # Exit: 21 trading days later close
            exit_close = bars[bar_idx + 1 + 21]['close']
            if entry_open <= 0:
                continue
            fwd_return = (exit_close - entry_open) / entry_open
            hit = 1 if fwd_return > 0 else 0
            calls.append((filed_ts, symbol_id, fwd_return, hit))

    if len(calls) < 30:
        print("INSUFFICIENT=1")
        return

    # Sort by decision time
    calls.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed
    split_idx = int(len(calls) * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0, 0, 0
        issued = len(call_list)
        hits = sum(c[3] for c in call_list)
        precision = hits / issued if issued else 0
        base_rate = precision  # within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(c[0]).date() for c in call_list))
        # Design effect: approximate by 1 + (avg calls per day - 1) * 0.5
        calls_per_day = issued / distinct_days if distinct_days else 1
        design_effect = 1 + (calls_per_day - 1) * 0.5
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(trades)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()