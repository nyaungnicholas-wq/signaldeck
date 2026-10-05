import sqlite3, math, statistics
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def percentile(data, pct):
    if not data:
        return None
    s = sorted(data)
    idx = (pct / 100) * (len(s) - 1)
    lo = int(idx)
    hi = lo + 1
    if hi >= len(s):
        return s[-1]
    return s[lo] + (s[hi] - s[lo]) * (idx - lo)

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.row_factory = sqlite3.Row
    except Exception as e:
        print(f"INSUFFICIENT=1\nError connecting: {e}")
        return

    try:
        cur = conn.cursor()
        cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
        symbol_ids = [row[0] for row in cur.fetchall()]
    except Exception as e:
        print(f"INSUFFICIENT=1\nError querying symbols: {e}")
        conn.close()
        return

    all_calls = []
    opportunities = 0
    processed = 0

    for sym_id in symbol_ids:
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT ts, open, high, low, close, volume "
                "FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts",
                (sym_id,)
            )
            rows = cur.fetchall()
        except:
            continue
        if len(rows) < 272:
            processed += 1
            continue

        ts_list = [r[0] for r in rows]
        close = [r[4] for r in rows]
        volume = [r[5] for r in rows]

        # Precompute 20-session realized volatility (using close-to-close returns)
        vol20 = [None] * len(rows)
        for i in range(20, len(rows)):
            rets = []
            for j in range(i-20, i):
                if close[j] > 0:
                    rets.append(close[j+1] / close[j] - 1)
            if len(rets) >= 2:
                vol20[i] = statistics.stdev(rets)

        last_call_idx = -21  # allow 20-day cooldown
        for i in range(271, len(rows)):
            # Universe checks
            if close[i] < 5.0:
                continue
            # Average daily dollar volume >= $5M over T-60..T-1
            sum_dollar_vol = 0.0
            for j in range(i-60, i):
                sum_dollar_vol += close[j] * volume[j]
            avg_dollar_vol = sum_dollar_vol / 60.0
            if avg_dollar_vol < 5_000_000:
                continue

            opportunities += 1

            # Check required bars exist (index-based)
            if i - 251 < 0 or i - 59 < 0 or i - 19 < 0:
                continue

            # Entry conditions
            # 1. New 52-week high
            max_close_252 = max(close[i-252:i])
            if not (close[i] > max_close_252):
                continue

            # 2. Volume bottom quartile of T-60..T-1
            vol_window = volume[i-60:i]
            if not vol_window:
                continue
            vol_q25 = percentile(vol_window, 25)
            if vol_q25 is None or volume[i] > vol_q25:
                continue

            # 3. 20-session realized volatility bottom quartile of T-252..T-1
            if vol20[i] is None:
                continue
            vol20_window = []
            for j in range(i-252, i):
                if vol20[j] is not None:
                    vol20_window.append(vol20[j])
            if not vol20_window:
                continue
            vol20_q25 = percentile(vol20_window, 25)
            if vol20_q25 is None or vol20[i] > vol20_q25:
                continue

            # 4. 20-session return between -2% and +2%
            ret20 = close[i] / close[i-20] - 1.0
            if abs(ret20) > 0.02:
                continue

            # 5. Close-to-close return between -0.5% and +0.5%
            ret1 = close[i] / close[i-1] - 1.0
            if abs(ret1) > 0.005:
                continue

            # Abstain if call issued for same symbol in prior 20 trading days
            if i - last_call_idx < 20:
                continue

            # Issue UP call
            all_calls.append({
                'sym_id': sym_id,
                'ts': ts_list[i],
                'idx': i
            })
            last_call_idx = i

        processed += 1
        if processed % 100 == 0:
            # Keep process alive
            pass

    if not all_calls:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Sort calls by ts
    all_calls.sort(key=lambda x: x['ts'])

    # Split into non-sealed (80%) and sealed (20%)
    n_total = len(all_calls)
    split_idx = int(n_total * 0.8)
    non_sealed = all_calls[:split_idx]
    sealed = all_calls[split_idx:]

    # Get labels for non-sealed calls
    hits = 0
    base_pos = 0  # count of positive outcomes in non-sealed issued calls
    distinct_days = set()
    day_counts = defaultdict(int)
    labeled_non_sealed = []

    for call in non_sealed:
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT up FROM prediction_outcomes "
                "WHERE symbol_id=? AND horizon=20 AND ts=?",
                (call['sym_id'], call['ts'])
            )
            row = cur.fetchone()
            if row is None:
                continue  # skip unlabeled call
            up = row[0]
            call['up'] = up
            labeled_non_sealed.append(call)
            if up == 1:
                hits += 1
                base_pos += 1
            day = call['ts'] // 86400  # approximate UTC day
            distinct_days.add(day)
            day_counts[day] += 1
        except:
            continue

    if len(labeled_non_sealed) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return

    issued = len(labeled_non_sealed)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = base_pos / issued if issued > 0 else 0.0
    distinct_days_count = len(distinct_days)

    # Effective sample size (design effect)
    N = issued
    sum_sq = 0.0
    for cnt in day_counts.values():
        sum_sq += cnt * cnt
    design_effect = sum_sq / N if N > 0 else 1.0
    effective_n = N / design_effect

    # Get labels for sealed calls
    sealed_hits = 0
    sealed_labeled = []
    for call in sealed:
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT up FROM prediction_outcomes "
                "WHERE symbol_id=? AND horizon=20 AND ts=?",
                (call['sym_id'], call['ts'])
            )
            row = cur.fetchone()
            if row is None:
                continue
            up = row[0]
            sealed_labeled.append(up)
            if up == 1:
                sealed_hits += 1
        except:
            continue

    sealed_precision = sealed_hits / len(sealed_labeled) if sealed_labeled else 0.0

    # Invariants
    if distinct_days_count > issued:
        print("INSUFFICIENT=1")
        conn.close()
        return
    if effective_n >= issued:
        print("INSUFFICIENT=1")
        conn.close()
        return

    conn.close()

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_count}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()