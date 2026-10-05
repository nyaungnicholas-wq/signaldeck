import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, date
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    # Load symbols
    symbols = {}
    cur = conn.execute("SELECT id, symbol, market, active, delisted_at FROM symbols")
    for row in cur:
        symbols[row['id']] = dict(row)

    # Load sentiment_features
    sentiment = defaultdict(dict)
    cur = conn.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
    for row in cur:
        sentiment[row['symbol_id']][row['day']] = row['mean_score']

    symbol_ids_with_sentiment = list(sentiment.keys())
    if not symbol_ids_with_sentiment:
        print("INSUFFICIENT=1")
        return 0

    placeholders = ','.join('?' * len(symbol_ids_with_sentiment))
    bars = defaultdict(dict)
    cur = conn.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
    """, symbol_ids_with_sentiment)
    for row in cur:
        dt = datetime.utcfromtimestamp(row['ts']).date()
        date_str = dt.isoformat()
        bars[row['symbol_id']][date_str] = (row['open'], row['high'], row['low'], row['close'], row['volume'])

    # For each symbol, get sorted dates where both bars and sentiment exist
    symbol_dates = {}
    for sid in symbol_ids_with_sentiment:
        bar_dates = set(bars[sid].keys())
        sent_dates = set(sentiment[sid].keys())
        common = sorted(bar_dates & sent_dates)
        if len(common) >= 253:  # need T + 252 prior
            symbol_dates[sid] = common

    if not symbol_dates:
        print("INSUFFICIENT=1")
        return 0

    # Precompute 20-day realized volatility for each symbol/date
    # vol_20[sid][date] = 20-day realized vol (std of log returns * sqrt(252))
    vol_20 = defaultdict(dict)
    for sid, dates in symbol_dates.items():
        closes = [bars[sid][d][3] for d in dates]
        log_rets = []
        for i in range(1, len(closes)):
            if closes[i-1] > 0 and closes[i] > 0:
                log_rets.append(math.log(closes[i] / closes[i-1]))
            else:
                log_rets.append(0.0)
        # rolling 20-day std
        for i in range(20, len(log_rets)):
            window = log_rets[i-20:i]
            mean = sum(window) / 20
            var = sum((x - mean) ** 2 for x in window) / 20
            std = math.sqrt(var) * math.sqrt(252)
            vol_20[sid][dates[i]] = std

    # Precompute cross-sectional 90th percentile of vol_20 for each date
    # For each date, collect all symbols' vol_20, compute 90th percentile
    date_vols = defaultdict(list)
    for sid, dvol in vol_20.items():
        for d, v in dvol.items():
            date_vols[d].append(v)

    date_vol_90 = {}
    for d, vols in date_vols.items():
        if len(vols) >= 10:
            vols_sorted = sorted(vols)
            idx = int(0.9 * (len(vols) - 1))
            date_vol_90[d] = vols_sorted[idx]
        else:
            date_vol_90[d] = float('inf')

    # Precompute rolling 5th percentile of sentiment for each symbol
    sent_p5 = defaultdict(dict)
    for sid, dates in symbol_dates.items():
        sent_vals = [sentiment[sid][d] for d in dates if d in sentiment[sid]]
        sent_dates = [d for d in dates if d in sentiment[sid]]
        for i in range(252, len(sent_vals)):
            window = sent_vals[i-252:i]
            if len(window) == 252:
                window_sorted = sorted(window)
                idx = int(0.05 * 251)
                sent_p5[sid][sent_dates[i]] = window_sorted[idx]

    # Precompute average dollar volume over 60 days for each symbol/date
    avg_dv_60 = defaultdict(dict)
    for sid, dates in symbol_dates.items():
        for i in range(60, len(dates)):
            window_dates = dates[i-60:i]
            total_dv = 0.0
            count = 0
            for d in window_dates:
                o, h, l, c, v = bars[sid][d]
                total_dv += c * v
                count += 1
            if count == 60:
                avg_dv_60[sid][dates[i]] = total_dv / 60

    # Now evaluate each decision point
    opportunities = []
    issued_calls = []
    last_issued = defaultdict(lambda: None)  # sid -> last issued date index

    for sid, dates in symbol_dates.items():
        for i, T in enumerate(dates):
            # Universe checks
            if i < 252:
                continue
            if i < 60:
                continue
            if i < 20:
                continue
            if i < 5:
                continue
            # Need T+5 for label
            if i + 5 >= len(dates):
                continue

            close_T = bars[sid][T][3]
            if close_T < 5.0:
                continue

            if T not in avg_dv_60[sid]:
                continue
            if avg_dv_60[sid][T] < 5_000_000:
                continue

            if T not in sentiment[sid]:
                continue
            if T not in sent_p5[sid]:
                continue

            # This is an opportunity
            opportunities.append((sid, T, i))

            # Entry conditions
            sent_T = sentiment[sid][T]
            p5_T = sent_p5[sid][T]
            if sent_T > p5_T:  # not in bottom 5%
                continue

            # Close-to-close return at T
            prev_date = dates[i-1]
            close_prev = bars[sid][prev_date][3]
            if close_prev <= 0:
                continue
            ret_T = (close_T - close_prev) / close_prev
            if ret_T > -0.03:
                continue

            # T's return is minimum of T-5..T-1
            rets_window = []
            for j in range(i-5, i):
                d = dates[j]
                d_prev = dates[j-1]
                c = bars[sid][d][3]
                c_prev = bars[sid][d_prev][3]
                if c_prev > 0:
                    rets_window.append((c - c_prev) / c_prev)
                else:
                    rets_window.append(float('inf'))
            if not rets_window or ret_T > min(rets_window):
                continue

            # Abstain: 20-session realized volatility at T in top cross-sectional decile
            if T in vol_20[sid] and T in date_vol_90:
                if vol_20[sid][T] > date_vol_90[T]:
                    continue

            # Abstain: call issued for same symbol in prior 20 trading days
            last_idx = last_issued[sid]
            if last_idx is not None and (i - last_idx) <= 20:
                continue

            # All conditions met - issue call
            # Label: T+5 close-to-close return
            T5 = dates[i+5]
            close_T5 = bars[sid][T5][3]
            label_up = 1 if close_T5 > close_T else 0

            issued_calls.append({
                'sid': sid,
                'date': T,
                'date_idx': i,
                'label': label_up,
                'close_T': close_T,
                'close_T5': close_T5
            })
            last_issued[sid] = i

    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        return 0

    if not issued_calls:
        print("ISSUED=0")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print("PRECISION=0.0")
        # Base rate in opportunities
        # Need to compute labels for opportunities
        opp_labels = []
        for sid, T, i in opportunities:
            if i + 5 < len(symbol_dates[sid]):
                T5 = symbol_dates[sid][i+5]
                close_T = bars[sid][T][3]
                close_T5 = bars[sid][T5][3]
                opp_labels.append(1 if close_T5 > close_T else 0)
        base_rate = sum(opp_labels) / len(opp_labels) if opp_labels else 0.0
        print(f"BASE_RATE={base_rate:.6f}")
        print("DISTINCT_DAYS=0")
        print("EFFECTIVE_N=0.0")
        print("SEALED_PRECISION=0.0")
        return 0

    # Split into sealed era (most recent 20% of opportunities by date)
    # Sort opportunities by date
    opportunities_sorted = sorted(opportunities, key=lambda x: x[1])
    split_idx = int(len(opportunities_sorted) * 0.8)
    main_opps = opportunities_sorted[:split_idx]
    sealed_opps = opportunities_sorted[split_idx:]

    main_set = set((sid, T) for sid, T, _ in main_opps)
    sealed_set = set((sid, T) for sid, T, _ in sealed_opps)

    main_issued = [c for c in issued_calls if (c['sid'], c['date']) in main_set]
    sealed_issued = [c for c in issued_calls if (c['sid'], c['date']) in sealed_set]

    # Precision
    hits_main = sum(c['label'] for c in main_issued)
    precision_main = hits_main / len(main_issued) if main_issued else 0.0

    hits_sealed = sum(c['label'] for c in sealed_issued)
    precision_sealed = hits_sealed / len(sealed_issued) if sealed_issued else 0.0

    # Base rate in opportunities (main era)
    main_opp_labels = []
    for sid, T, i in main_opps:
        if i + 5 < len(symbol_dates[sid]):
            T5 = symbol_dates[sid][i+5]
            close_T = bars[sid][T][3]
            close_T5 = bars[sid][T5][3]
            main_opp_labels.append(1 if close_T5 > close_T else 0)
    base_rate_main = sum(main_opp_labels) / len(main_opp_labels) if main_opp_labels else 0.0

    # Distinct days among issued calls (all issued, not just main)
    issued_dates = set(c['date'] for c in issued_calls)
    distinct_days = len(issued_dates)

    # Design effect: sum(n_d^2) / sum(n_d) where n_d = calls per day
    day_counts = defaultdict(int)
    for c in issued_calls:
        day_counts[c['date']] += 1
    sum_n2 = sum(v * v for v in day_counts.values())
    sum_n = len(issued_calls)
    design_effect = sum_n2 / sum_n if sum_n > 0 else 1.0
    effective_n = sum_n / design_effect if design_effect > 0 else 0.0

    print(f"ISSUED={len(issued_calls)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision_main:.6f}")
    print(f"BASE_RATE={base_rate_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())