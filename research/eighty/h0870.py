# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 869
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get officer open-market purchases (code='P') with filed_ts
    cur.execute("""
        SELECT symbol_id, filed_ts, title
        FROM insider_trades
        WHERE code = 'P'
          AND filed_ts IS NOT NULL
          AND (
              title LIKE '%CEO%' OR title LIKE '%CFO%' 
              OR title LIKE '%CHIEF EXECUTIVE%' OR title LIKE '%CHIEF FINANCIAL%'
              OR title LIKE '%CHIEF OPERATING%' OR title LIKE '%PRESIDENT%'
          )
        ORDER BY symbol_id, filed_ts
    """)
    filings = cur.fetchall()
    if not filings:
        print("INSUFFICIENT=1")
        return

    # Group by symbol
    by_symbol = {}
    for row in filings:
        by_symbol.setdefault(row['symbol_id'], []).append((row['filed_ts'], row['title']))

    symbols = list(by_symbol.keys())
    placeholders = ','.join('?' * len(symbols))
    
    # Get daily bars for these symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE symbol_id IN ({placeholders}) AND tf = '1d'
        ORDER BY symbol_id, ts
    """, symbols)
    bars_rows = cur.fetchall()

    # Organize bars by symbol
    bars_by_symbol = {}
    for row in bars_rows:
        bars_by_symbol.setdefault(row['symbol_id'], []).append((row['ts'], row['close']))

    signals = []  # (decision_ts, symbol_id, forward_return, label)

    for sym_id, filings_list in by_symbol.items():
        bars = bars_by_symbol.get(sym_id, [])
        if len(bars) < 300:  # Need enough history for 252-day window + 63-day return + 21-day forward
            continue

        # Extract arrays
        ts_list = [b[0] for b in bars]
        close_list = [b[1] for b in bars]
        n = len(close_list)

        # Daily returns
        rets = [0.0] * n
        for i in range(1, n):
            if close_list[i-1] > 0:
                rets[i] = (close_list[i] - close_list[i-1]) / close_list[i-1]

        # 21-day realized volatility (annualized)
        rv21 = [0.0] * n
        for i in range(21, n):
            window = rets[i-20:i+1]
            mean = sum(window) / 21
            var = sum((x - mean) ** 2 for x in window) / 20
            rv21[i] = math.sqrt(var * 252)

        # Rolling 252-day 90th percentile of rv21
        pct90 = [0.0] * n
        for i in range(252, n):
            window = rv21[i-251:i+1]
            sorted_win = sorted(window)
            idx = int(0.9 * len(sorted_win))
            pct90[i] = sorted_win[min(idx, len(sorted_win)-1)]

        # Identify breakout days: rv21 > pct90 AND prev rv21 <= prev pct90
        breakout_days = set()
        for i in range(253, n):
            if rv21[i] > pct90[i] and rv21[i-1] <= pct90[i-1]:
                breakout_days.add(i)

        # 63-day return at each day
        ret63 = [0.0] * n
        for i in range(63, n):
            if close_list[i-63] > 0:
                ret63[i] = (close_list[i] - close_list[i-63]) / close_list[i-63]

        # Map filing to trading day index (first trading day with ts > filed_ts)
        for filed_ts, title in filings_list:
            # Find decision index: first bar with ts > filed_ts
            dec_idx = None
            for idx, ts in enumerate(ts_list):
                if ts > filed_ts:
                    dec_idx = idx
                    break
            if dec_idx is None or dec_idx + 21 >= n:
                continue

            # Check if any breakout in prior 5 trading days (dec_idx-5 to dec_idx)
            has_breakout = False
            breakout_idx = -1
            for lookback in range(0, 6):
                check_idx = dec_idx - lookback
                if check_idx in breakout_days:
                    has_breakout = True
                    breakout_idx = check_idx
                    break
            if not has_breakout:
                continue

            # Check 63-day return negative at breakout
            if ret63[breakout_idx] >= 0:
                continue

            # Compute 21-day forward return from decision
            fwd_ret = (close_list[dec_idx + 21] - close_list[dec_idx]) / close_list[dec_idx]
            label = 1 if fwd_ret > 0 else 0
            signals.append((filed_ts, sym_id, fwd_ret, label, dec_idx))

    if not signals:
        print("INSUFFICIENT=1")
        return

    # Sort by decision time
    signals.sort(key=lambda x: x[0])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(signals) * 0.8)
    train_signals = signals[:split_idx]
    sealed_signals = signals[split_idx:]

    def compute_metrics(sig_list, label):
        if not sig_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(sig_list)
        hits = sum(1 for s in sig_list if s[3] == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(s[0]).date() for s in sig_list))
        
        # Design effect: 1 + (avg cluster size - 1) * intraclass correlation
        # Approximate: group by day, compute variance of daily counts
        day_counts = {}
        for s in sig_list:
            day = datetime.utcfromtimestamp(s[0]).date()
            day_counts[day] = day_counts.get(day, 0) + 1
        counts = list(day_counts.values())
        if len(counts) > 1:
            mean_c = sum(counts) / len(counts)
            var_c = sum((c - mean_c) ** 2 for c in counts) / (len(counts) - 1)
            icc = var_c / (mean_c * (mean_c + var_c)) if (mean_c * (mean_c + var_c)) > 0 else 0
            deff = 1 + (mean_c - 1) * icc if mean_c > 1 else 1
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_OPPORTUNITIES={issued}")  # Each signal is an opportunity considered
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.2f}")
        return issued, hits, precision, base_rate, distinct_days, effective_n

    print("TRAIN_ERA=1")
    compute_metrics(train_signals, "TRAIN")
    print("SEALED_ERA=1")
    compute_metrics(sealed_signals, "SEALED")

    # Final required output format
    total_issued = len(signals)
    total_hits = sum(1 for s in signals if s[3] == 1)
    precision = total_hits / total_issued if total_issued > 0 else 0.0
    base_rate = precision
    distinct_days = len(set(datetime.utcfromtimestamp(s[0]).date() for s in signals))
    
    day_counts = {}
    for s in signals:
        day = datetime.utcfromtimestamp(s[0]).date()
        day_counts[day] = day_counts.get(day, 0) + 1
    counts = list(day_counts.values())
    if len(counts) > 1:
        mean_c = sum(counts) / len(counts)
        var_c = sum((c - mean_c) ** 2 for c in counts) / (len(counts) - 1)
        icc = var_c / (mean_c * (mean_c + var_c)) if (mean_c * (mean_c + var_c)) > 0 else 0
        deff = 1 + (mean_c - 1) * icc if mean_c > 1 else 1
    else:
        deff = 1.0
    effective_n = total_issued / deff if deff > 0 else total_issued
    
    sealed_precision = 0.0
    if sealed_signals:
        sealed_hits = sum(1 for s in sealed_signals if s[3] == 1)
        sealed_precision = sealed_hits / len(sealed_signals)

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_issued}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    conn.close()

if __name__ == '__main__':
    main()