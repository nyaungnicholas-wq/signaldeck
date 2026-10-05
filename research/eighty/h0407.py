# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 406
# cycle_index: 74
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import time
from datetime import datetime, UTC
import bisect

def main():
    start_time = time.time()
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # ---- 1. Discover horizons in prediction_outcomes ----
    cur.execute("SELECT horizon, COUNT(*) as cnt FROM prediction_outcomes GROUP BY horizon ORDER BY cnt DESC")
    horizons = cur.fetchall()
    if not horizons:
        print("INSUFFICIENT=1")
        return
    target_horizon = horizons[0]['horizon']
    print(f"Using horizon={target_horizon} (most common: {horizons[0]['cnt']} rows)", file=sys.stderr)

    # ---- 2. Get available FRED series ----
    cur.execute("SELECT DISTINCT series FROM macro_series")
    fred_series = {row['series'] for row in cur.fetchall()}
    term_spread_series = None
    vix_series = None
    for s in fred_series:
        if 'T10Y2Y' in s or 'T10Y3M' in s or 'TERM' in s.upper():
            term_spread_series = s
        if 'VIX' in s.upper():
            vix_series = s
    if not term_spread_series or not vix_series:
        print(f"INSUFFICIENT=1: required macro series not found (term={term_spread_series}, vix={vix_series})", file=sys.stderr)
        print("INSUFFICIENT=1")
        return
    print(f"Macro series: term={term_spread_series}, vix={vix_series}", file=sys.stderr)

    # ---- 3. Get prediction_outcomes for target horizon with date range ----
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = ? AND up IS NOT NULL
        ORDER BY ts
    """, (target_horizon,))
    outcomes = cur.fetchall()
    if len(outcomes) < 100:
        print("INSUFFICIENT=1")
        return

    # Split into sealed (most recent 20%) and training (older 80%)
    split_idx = int(len(outcomes) * 0.8)
    train_outcomes = outcomes[:split_idx]
    sealed_outcomes = outcomes[split_idx:]

    # ---- 4. Build macro features (latest value before each decision_ts) ----
    cur.execute("SELECT ts, value FROM macro_series WHERE series = ? ORDER BY ts", (term_spread_series,))
    term_data = cur.fetchall()
    cur.execute("SELECT ts, value FROM macro_series WHERE series = ? ORDER BY ts", (vix_series,))
    vix_data = cur.fetchall()

    term_ts = [r['ts'] for r in term_data]
    term_val = [r['value'] for r in term_data]
    vix_ts = [r['ts'] for r in vix_data]
    vix_val = [r['value'] for r in vix_data]

    def get_macro(ts_arr, val_arr, decision_ts):
        i = bisect.bisect_left(ts_arr, decision_ts) - 1
        if i >= 0:
            return val_arr[i]
        return None

    # ---- 5. Get active symbols ----
    cur.execute("SELECT id FROM symbols WHERE active = 1")
    active_symbols = {row['id'] for row in cur.fetchall()}

    # ---- 6. Pre-load bars for active symbols (1d only) ----
    symbol_ids_in_outcomes = set(r['symbol_id'] for r in outcomes) & active_symbols
    if not symbol_ids_in_outcomes:
        print("INSUFFICIENT=1")
        return

    placeholders = ','.join('?' * len(symbol_ids_in_outcomes))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, tuple(symbol_ids_in_outcomes))
    bars_by_symbol = {}
    for row in cur.fetchall():
        bars_by_symbol.setdefault(row['symbol_id'], []).append((row['ts'], row['close']))

    # ---- 7. Pre-load sentiment_features for relevant symbols ----
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, tuple(symbol_ids_in_outcomes))
    sent_by_symbol = {}
    for row in cur.fetchall():
        dt = datetime.strptime(row['day'], '%Y-%m-%d')
        ts = int(dt.replace(tzinfo=UTC).timestamp())
        sent_by_symbol.setdefault(row['symbol_id'], []).append((ts, row['mean_score']))

    # ---- 8. Compute thresholds on training set for news sentiment ----
    train_sent_vals = []
    for row in train_outcomes:
        sym = row['symbol_id']
        decision_ts = row['ts']
        decision_dt = datetime.fromtimestamp(decision_ts, UTC)
        decision_day_ts = int(datetime(decision_dt.year, decision_dt.month, decision_dt.day, tzinfo=UTC).timestamp())
        if sym not in sent_by_symbol:
            continue
        sent_list = sent_by_symbol[sym]
        vals = [v for t, v in sent_list if t < decision_day_ts][-20:]
        if len(vals) >= 10:
            train_sent_vals.append(sum(vals) / len(vals))

    if not train_sent_vals:
        print("INSUFFICIENT=1")
        return

    train_sent_vals.sort()
    sent_threshold = train_sent_vals[len(train_sent_vals) // 5]  # 20th percentile

    # ---- 9. Evaluate signal on all outcomes (train + sealed) ----
    def eval_outcome(row, is_sealed):
        sym = row['symbol_id']
        decision_ts = row['ts']
        actual_up = row['up']
        if actual_up is None:
            return None
        decision_dt = datetime.fromtimestamp(decision_ts, UTC)
        decision_day_ts = int(datetime(decision_dt.year, decision_dt.month, decision_dt.day, tzinfo=UTC).timestamp())

        term_spread = get_macro(term_ts, term_val, decision_ts)
        vix = get_macro(vix_ts, vix_val, decision_ts)
        if term_spread is None or vix is None:
            return None

        if sym not in bars_by_symbol or len(bars_by_symbol[sym]) < 22:
            return None
        bars = bars_by_symbol[sym]
        bar_ts = [b[0] for b in bars]
        i = bisect.bisect_left(bar_ts, decision_ts) - 1
        if i < 20:
            return None
        close_now = bars[i][1]
        close_20d = bars[i-20][1]
        if close_20d <= 0:
            return None
        ret_20d = (close_now / close_20d) - 1.0

        if sym not in sent_by_symbol:
            return None
        sent_list = sent_by_symbol[sym]
        sent_vals = [v for t, v in sent_list if t < decision_day_ts][-20:]
        if len(sent_vals) < 10:
            return None
        avg_sent = sum(sent_vals) / len(sent_vals)

        signal = (
            avg_sent < sent_threshold and
            ret_20d > -0.05 and
            term_spread > 0 and
            vix < 25
        )

        return {
            'signal': signal,
            'actual_up': actual_up,
            'decision_ts': decision_ts,
            'decision_day': decision_day_ts,
            'is_sealed': is_sealed
        }

    results = []
    for row in train_outcomes:
        res = eval_outcome(row, False)
        if res:
            results.append(res)
    for row in sealed_outcomes:
        res = eval_outcome(row, True)
        if res:
            results.append(res)

    if not results:
        print("INSUFFICIENT=1")
        return

    # ---- 10. Compute metrics ----
    opportunities = len(results)
    issued = [r for r in results if r['signal']]
    issued_count = len(issued)
    if issued_count == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(1 for r in issued if r['actual_up'] == 1)
    precision = hits / issued_count

    base_rate = sum(r['actual_up'] for r in issued) / issued_count

    distinct_days = len(set(r['decision_day'] for r in issued))

    avg_per_day = issued_count / distinct_days if distinct_days > 0 else 1
    design_effect = 1 + (avg_per_day - 1) * 0.5
    if design_effect <= 1:
        design_effect = 1.01
    effective_n = issued_count / design_effect

    sealed_issued = [r for r in issued if r['is_sealed']]
    sealed_precision = 0.0
    if sealed_issued:
        sealed_hits = sum(1 for r in sealed_issued if r['actual_up'] == 1)
        sealed_precision = sealed_hits / len(sealed_issued)

    # ---- 11. Output ----
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    elapsed = time.time() - start_time
    print(f"Elapsed: {elapsed:.1f}s", file=sys.stderr)

if __name__ == '__main__':
    main()