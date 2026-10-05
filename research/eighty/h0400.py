# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 399
# cycle_index: 67
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, date
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load sentiment features: symbol_id, day (YYYY-MM-DD), mean_score
    cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day")
    sentiment_rows = cur.fetchall()
    if not sentiment_rows:
        print("INSUFFICIENT=1")
        return

    # Load daily bars: symbol_id, ts (unix epoch), close
    cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bar_rows = cur.fetchall()
    if not bar_rows:
        print("INSUFFICIENT=1")
        return

    # Organize by symbol_id
    sentiment_by_symbol = defaultdict(list)
    for row in sentiment_rows:
        try:
            d = datetime.strptime(row['day'], '%Y-%m-%d').date()
            sentiment_by_symbol[row['symbol_id']].append((d, row['mean_score']))
        except (ValueError, TypeError):
            continue

    bars_by_symbol = defaultdict(list)
    for row in bar_rows:
        try:
            d = datetime.utcfromtimestamp(row['ts']).date()
            close = row['close']
            if close is not None and close > 0:
                bars_by_symbol[row['symbol_id']].append((d, close))
        except (ValueError, TypeError, OSError):
            continue

    # Find common symbols with both sentiment and price data
    common_symbols = set(sentiment_by_symbol.keys()) & set(bars_by_symbol.keys())
    if not common_symbols:
        print("INSUFFICIENT=1")
        return

    all_opportunities = []  # (decision_date, symbol_id, is_entry, hit)

    for sym in common_symbols:
        sent = sentiment_by_symbol[sym]
        bars = bars_by_symbol[sym]
        if len(sent) < 252 + 60 + 63 or len(bars) < 252 + 60 + 63:
            continue

        # Create date-indexed maps for fast lookup
        sent_map = {d: v for d, v in sent}
        bars_map = {d: v for d, v in bars}

        # Get sorted dates present in BOTH
        common_dates = sorted(set(sent_map.keys()) & set(bars_map.keys()))
        if len(common_dates) < 252 + 60 + 63:
            continue

        # For each potential decision date (index i in common_dates)
        for i in range(252, len(common_dates) - 63):
            decision_date = common_dates[i]

            # Universe filter: at least 252 sentiment days prior to decision_date
            # (already ensured by i >= 252 since common_dates are sorted)

            # 60-day lookback window: indices i-59 to i (inclusive = 60 days)
            window_dates = common_dates[i-59:i+1]
            if len(window_dates) != 60:
                continue

            # Collect sentiment values in window
            window_sentiments = []
            first_5_sentiments = []
            last_5_sentiments = []
            for j, d in enumerate(window_dates):
                val = sent_map.get(d)
                if val is not None:
                    window_sentiments.append(val)
                    if j < 5:
                        first_5_sentiments.append(val)
                    if j >= 55:
                        last_5_sentiments.append(val)

            if len(window_sentiments) < 40:
                continue
            if not first_5_sentiments or not last_5_sentiments:
                continue

            # Compute sentiment improvement
            mean_first_5 = sum(first_5_sentiments) / len(first_5_sentiments)
            mean_last_5 = sum(last_5_sentiments) / len(last_5_sentiments)
            std_sent = math.sqrt(sum((x - (sum(window_sentiments)/len(window_sentiments)))**2 for x in window_sentiments) / len(window_sentiments))
            if std_sent == 0:
                continue
            improvement = (mean_last_5 - mean_first_5) / std_sent

            # 60-day cumulative log return
            close_start = bars_map.get(window_dates[0])
            close_end = bars_map.get(window_dates[-1])
            if close_start is None or close_end is None or close_start <= 0:
                continue
            ret_60 = math.log(close_end / close_start)

            # Entry conditions
            is_entry = (improvement > 1.0) and (ret_60 < 0.05)

            # Label: 63-day forward return direction
            future_date = common_dates[i + 63]
            close_future = bars_map.get(future_date)
            if close_future is None or close_future <= 0:
                continue
            hit = 1 if close_future > close_end else 0

            all_opportunities.append((decision_date, sym, is_entry, hit))

    if not all_opportunities:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_date
    all_opportunities.sort(key=lambda x: x[0])

    # Split: most recent 20% as sealed era
    split_idx = int(len(all_opportunities) * 0.8)
    train_opps = all_opportunities[:split_idx]
    sealed_opps = all_opportunities[split_idx:]

    def compute_metrics(opps):
        issued = [o for o in opps if o[2]]  # is_entry
        if not issued:
            return {
                'issued': 0,
                'opportunities': len(opps),
                'precision': 0.0,
                'base_rate': 0.0,
                'distinct_days': 0,
                'effective_n': 0.0
            }

        hits = sum(o[3] for o in issued)
        precision = hits / len(issued)
        base_rate = hits / len(issued)  # base rate within issued subset = precision for binary? Wait.
        # Base rate of predicted class (up=1) within issued subset
        base_rate = sum(o[3] for o in issued) / len(issued)

        distinct_days = len(set(o[0] for o in issued))

        # Design effect via day-level ICC
        day_groups = defaultdict(list)
        for o in issued:
            day_groups[o[0]].append(o[3])

        m_d = [len(v) for v in day_groups.values()]
        p_d = [sum(v)/len(v) for v in day_groups.values()]
        total_issued = len(issued)
        overall_p = sum(o[3] for o in issued) / total_issued

        if len(m_d) > 1:
            avg_m = sum(m_d) / len(m_d)
            within_var = sum(m * p * (1-p) for m, p in zip(m_d, p_d)) / total_issued
            between_var = sum(m * (p - overall_p)**2 for m, p in zip(m_d, p_d)) / total_issued
            denom = between_var + within_var
            icc = between_var / denom if denom > 0 else 0.0
            design_effect = 1 + (avg_m - 1) * icc
            if design_effect <= 1.0:
                design_effect = 1.01
        else:
            design_effect = 1.01

        effective_n = total_issued / design_effect

        return {
            'issued': total_issued,
            'opportunities': len(opps),
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_metrics = compute_metrics(train_opps)
    sealed_metrics = compute_metrics(sealed_opps)

    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={train_metrics['opportunities']}")
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")

if __name__ == '__main__':
    main()