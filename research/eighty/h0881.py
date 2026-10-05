# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 880
# cycle_index: 26
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

    # Get all symbols with enough daily bars and sentiment history
    # Exclude delisted before 2026-07-24 (survivor bias)
    cur.execute("""
        SELECT s.id, s.symbol, s.delisted_at
        FROM symbols s
        WHERE s.active = 1
          AND (s.delisted_at IS NULL OR s.delisted_at >= '2026-07-24')
    """)
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return

    symbol_ids = list(symbols.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # Get daily bars (tf='1d') for these symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_rows = cur.fetchall()

    # Get sentiment_features for these symbols
    cur.execute(f"""
        SELECT symbol_id, day, mean_score, n_all
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)
    sent_rows = cur.fetchall()

    # Organize bars by symbol: list of (date_str, close, ts)
    bars_by_sym = defaultdict(list)
    for row in bars_rows:
        dt = datetime.utcfromtimestamp(row['ts']).date()
        bars_by_sym[row['symbol_id']].append((dt.isoformat(), row['close'], row['ts']))

    # Organize sentiment by symbol: dict day -> (mean_score, n_all)
    sent_by_sym = defaultdict(dict)
    for row in sent_rows:
        sent_by_sym[row['symbol_id']][row['day']] = (row['mean_score'], row['n_all'])

    # For each symbol, align bars and sentiment by date
    # Compute features and signals
    all_opportunities = []  # (symbol_id, decision_date, decision_ts, features_dict)
    all_signals = []        # (symbol_id, decision_date, decision_ts, predicted_dir, fwd_return)

    # Need at least 252 daily bars and 60 sentiment rows for lookback
    min_bars = 252
    min_sent = 60
    lookback_price = 20
    lookback_sent = 5
    min_avg_nall = 3
    sent_slope_thresh = 0.1  # normalized slope threshold
    price_mom_thresh = 0.02  # 2%

    for sym_id in symbol_ids:
        bars = bars_by_sym.get(sym_id, [])
        sent = sent_by_sym.get(sym_id, {})
        if len(bars) < min_bars or len(sent) < min_sent:
            continue

        # Build aligned series: only dates present in both
        aligned = []
        for date_str, close, ts in bars:
            if date_str in sent:
                mean_score, n_all = sent[date_str]
                aligned.append((date_str, close, ts, mean_score, n_all))

        if len(aligned) < max(min_bars, min_sent) + lookback_price + lookback_sent + 5:
            continue

        # Precompute arrays for speed
        dates = [a[0] for a in aligned]
        closes = [a[1] for a in aligned]
        tss = [a[2] for a in aligned]
        sent_scores = [a[3] for a in aligned]
        n_all_vals = [a[4] for a in aligned]

        # Compute 20-day price momentum and 5-day sentiment slope for each day
        # Start from index where we have enough lookback
        start_idx = max(lookback_price, lookback_sent)
        end_idx = len(aligned) - 5  # need 5 days forward for label

        for i in range(start_idx, end_idx):
            # 20-day price momentum: close[i] / close[i-20] - 1
            price_mom = closes[i] / closes[i - lookback_price] - 1.0

            # 5-day sentiment slope (linear regression slope normalized)
            # Use simple difference: (sent[i] - sent[i-4]) / 4, normalized by std of last 5
            sent_window = sent_scores[i - lookback_sent + 1:i + 1]
            if len(sent_window) < lookback_sent:
                continue
            sent_slope = (sent_window[-1] - sent_window[0]) / (lookback_sent - 1)
            sent_std = (sum((x - sum(sent_window)/len(sent_window))**2 for x in sent_window) / len(sent_window))**0.5
            if sent_std > 0:
                sent_slope_norm = sent_slope / sent_std
            else:
                sent_slope_norm = 0.0

            # 5-day avg n_all
            nall_window = n_all_vals[i - lookback_sent + 1:i + 1]
            avg_nall = sum(nall_window) / len(nall_window)

            # Entry conditions
            if sent_slope_norm > sent_slope_thresh and abs(price_mom) < price_mom_thresh and avg_nall >= min_avg_nall:
                # Long call
                decision_date = dates[i]
                decision_ts = tss[i]
                # Forward 5-day return (1 week)
                fwd_return = closes[i + 5] / closes[i] - 1.0
                predicted_dir = 1  # up
                all_signals.append((sym_id, decision_date, decision_ts, predicted_dir, fwd_return))

            # Count as opportunity regardless of signal
            all_opportunities.append((sym_id, dates[i], tss[i]))

    if not all_signals:
        print("INSUFFICIENT=1")
        return

    # Split by time: hold out most recent 20% of signals as sealed era
    all_signals.sort(key=lambda x: x[2])  # sort by decision_ts
    split_idx = int(len(all_signals) * 0.8)
    train_signals = all_signals[:split_idx]
    sealed_signals = all_signals[split_idx:]

    def evaluate(signals):
        if not signals:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(signals)
        hits = sum(1 for s in signals if (s[3] == 1 and s[4] > 0) or (s[3] == -1 and s[4] < 0))
        precision = hits / issued if issued > 0 else 0.0
        # Base rate of predicted class within issued subset
        # For long-only, predicted class is "up", base rate = fraction of issued with fwd_return > 0
        base_rate = sum(1 for s in signals if s[4] > 0) / issued if issued > 0 else 0.0
        # Distinct UTC days among issued calls
        distinct_days = len(set(s[1] for s in signals))
        # Effective N: design effect from clustering by day
        # Compute intraclass correlation of outcomes within days
        day_outcomes = defaultdict(list)
        for s in signals:
            outcome = 1 if (s[3] == 1 and s[4] > 0) or (s[3] == -1 and s[4] < 0) else 0
            day_outcomes[s[1]].append(outcome)
        day_means = [sum(v)/len(v) for v in day_outcomes.values()]
        day_sizes = [len(v) for v in day_outcomes.values()]
        overall_mean = sum(day_means[i] * day_sizes[i] for i in range(len(day_means))) / sum(day_sizes) if day_sizes else 0
        # Between-day variance
        if len(day_means) > 1:
            between_var = sum(day_sizes[i] * (day_means[i] - overall_mean)**2 for i in range(len(day_means))) / (len(day_means) - 1)
            within_var = sum((v - day_means[i])**2 for i, vs in enumerate(day_outcomes.values()) for v in vs) / (sum(day_sizes) - len(day_means)) if sum(day_sizes) > len(day_means) else 0
            if within_var > 0:
                icc = between_var / (between_var + within_var)
            else:
                icc = 0.0
            avg_cluster = sum(day_sizes) / len(day_sizes) if day_sizes else 1
            design_effect = 1 + (avg_cluster - 1) * icc
            effective_n = issued / design_effect if design_effect > 0 else issued
        else:
            effective_n = issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_base, train_days, train_eff = evaluate(train_signals)
    sealed_issued, sealed_hits, sealed_prec, sealed_base, sealed_days, sealed_eff = evaluate(sealed_signals)

    # Overall metrics (on full set for reporting)
    all_issued, all_hits, all_prec, all_base, all_days, all_eff = evaluate(all_signals)

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={len(all_opportunities)}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_base:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={all_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()