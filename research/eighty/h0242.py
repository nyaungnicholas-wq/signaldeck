import sqlite3
import datetime

def ts_to_date(ts):
    return datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_ts(d):
    return int(datetime.datetime.strptime(d, '%Y-%m-%d').timestamp())

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception:
        print("INSUFFICIENT=1")
        return

    cur = conn.cursor()

    # Get all distinct trading days from daily bars
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    all_days = [row['ts'] for row in cur.fetchall()]
    if len(all_days) < 252:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # 80/20 split by time
    split_idx = int(len(all_days) * 0.8)
    train_days = all_days[:split_idx]
    sealed_days = all_days[split_idx:]

    # Get symbols that appear in both bars and sentiment_features
    cur.execute("""
        SELECT DISTINCT b.symbol_id
        FROM bars b
        JOIN sentiment_features s ON b.symbol_id = s.symbol_id
        WHERE b.tf='1d'
    """)
    common_symbols = [row['symbol_id'] for row in cur.fetchall()]
    if not common_symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return

    calls = []  # list of (ts, symbol_id, is_sealed, hit)
    last_call_day = {}  # symbol_id -> last call ts

    # Pre-fetch all daily bars for speed
    cur.execute("SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d'")
    all_bars = cur.fetchall()
    bars_by_sym = {}
    for row in all_bars:
        bars_by_sym.setdefault(row['symbol_id'], []).append(row)

    # Pre-fetch all sentiment
    cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
    all_sent = cur.fetchall()
    sent_by_sym = {}
    for row in all_sent:
        sent_by_sym.setdefault(row['symbol_id'], {})[row['day']] = row['mean_score']

    # Pre-compute 20-day realized volatility for each symbol at each day
    vol_by_sym = {}
    for sym, bars in bars_by_sym.items():
        if len(bars) < 21:
            continue
        bars.sort(key=lambda x: x['ts'])
        vols = []
        for i in range(20, len(bars)):
            rets = []
            for j in range(i-19, i+1):
                if j > 0:
                    r = (bars[j]['close'] - bars[j-1]['close']) / bars[j-1]['close']
                    rets.append(r)
            if len(rets) == 20:
                mean_ret = sum(rets) / 20
                var = sum((r - mean_ret)**2 for r in rets) / 19
                vol = var ** 0.5
            else:
                vol = None
            vols.append((bars[i]['ts'], vol))
        vol_by_sym[sym] = vols

    # For cross-sectional volatility decile, we need all symbols' vol at each decision day
    # Build a map: ts -> list of (sym, vol)
    vol_at_ts = {}
    for sym, vols in vol_by_sym.items():
        for ts, vol in vols:
            if vol is not None:
                vol_at_ts.setdefault(ts, []).append((sym, vol))

    # Process each day in chronological order
    for t_ts in all_days:
        t_date = ts_to_date(t_ts)
        is_sealed = t_ts in sealed_days

        # Get symbols with bar and sentiment on this day
        eligible = []
        for sym in common_symbols:
            if sym not in bars_by_sym or sym not in sent_by_sym:
                continue
            # Check bar exists
            bar = next((b for b in bars_by_sym[sym] if b['ts'] == t_ts), None)
            if not bar or bar['close'] < 5:
                continue
            # Check sentiment exists
            if t_date not in sent_by_sym[sym]:
                continue
            eligible.append(sym)

        for sym in eligible:
            opportunities = opportunities + 1 if 'opportunities' in locals() else 1

            # Check 252 prior sessions
            bars = bars_by_sym[sym]
            prior_bars = [b for b in bars if b['ts'] < t_ts]
            if len(prior_bars) < 252:
                continue

            # Dollar volume avg over T-60..T-1 >= $5M
            t_minus_60 = t_ts - 60 * 86400
            recent_bars = [b for b in prior_bars if b['ts'] > t_minus_60]
            if not recent_bars:
                continue
            avg_dollar_vol = sum(b['close'] * b['volume'] for b in recent_bars) / len(recent_bars)
            if avg_dollar_vol < 5e6:
                continue

            # Sentiment condition: T-4..T each <= 10th percentile over T-252..T-1
            sentiment_dates = [ts_to_date(t_ts - d * 86400) for d in range(5)]
            sent_vals = []
            for d in sentiment_dates:
                if d in sent_by_sym[sym]:
                    sent_vals.append(sent_by_sym[sym][d])
                else:
                    sent_vals.append(None)
            if any(v is None for v in sent_vals):
                continue

            # Get historical sentiment for percentile
            t_minus_252_date = ts_to_date(t_ts - 252 * 86400)
            hist_sent = [v for day, v in sent_by_sym[sym].items() if t_minus_252_date <= day < t_date]
            if len(hist_sent) < 30:
                continue
            hist_sent_sorted = sorted(hist_sent)
            idx = int(len(hist_sent_sorted) * 0.1)
            p10 = hist_sent_sorted[idx]

            if any(v > p10 for v in sent_vals):
                continue

            # Close within [-3%, +1%] of T-5 close
            t_minus_5_ts = t_ts - 5 * 86400
            bar_t5 = next((b for b in prior_bars if b['ts'] == t_minus_5_ts), None)
            bar_t = next((b for b in prior_bars if b['ts'] == t_ts), None)
            if not bar_t5 or not bar_t:
                continue
            if not (bar_t5['close'] * 0.97 <= bar_t['close'] <= bar_t5['close'] * 1.01):
                continue

            # Volume >= 1.5x avg over T-60..T-1
            if not recent_bars:
                continue
            avg_vol = sum(b['volume'] for b in recent_bars) / len(recent_bars)
            if bar_t['volume'] < 1.5 * avg_vol:
                continue

            # 20-day realized volatility not in top cross-sectional decile
            vol_list = vol_at_ts.get(t_ts, [])
            if not vol_list:
                continue
            # Find this symbol's vol
            sym_vol = next((v for s, v in vol_list if s == sym), None)
            if sym_vol is None:
                continue
            # Cross-sectional decile
            all_vols = sorted([v for s, v in vol_list])
            decile_idx = int(len(all_vols) * 0.9)
            if decile_idx >= len(all_vols):
                decile_idx = len(all_vols) - 1
            top_decile_threshold = all_vols[decile_idx]
            if sym_vol > top_decile_threshold:
                continue

            # No call in prior 20 trading days
            last_ts = last_call_day.get(sym)
            if last_ts is not None:
                # Count trading days between last_ts and t_ts
                bars_between = [b for b in bars if last_ts < b['ts'] < t_ts]
                if len(bars_between) < 20:
                    continue

            # All conditions met - issue call
            # Get label from prediction_outcomes for T+20
            cur.execute("""
                SELECT up FROM prediction_outcomes
                WHERE symbol_id=? AND horizon=20 AND ts=?
            """, (sym, t_ts))
            row = cur.fetchone()
            if not row or row['up'] is None:
                continue
            hit = 1 if row['up'] == 1 else 0

            calls.append((t_ts, sym, is_sealed, hit))
            last_call_day[sym] = t_ts

    if not calls:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Separate train and sealed
    train_calls = [c for c in calls if not c[2]]
    sealed_calls = [c for c in calls if c[2]]

    issued = len(calls)
    train_issued = len(train_calls)
    sealed_issued = len(sealed_calls)

    if train_issued == 0 and sealed_issued == 0:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Precision
    train_hits = sum(c[3] for c in train_calls)
    sealed_hits = sum(c[3] for c in sealed_calls)
    train_precision = train_hits / train_issued if train_issued else 0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0

    # Base rate within issued subset (use train for claim evaluation)
    base_rate = train_hits / train_issued if train_issued else 0

    # Distinct days among issued calls
    issued_days = set(c[0] for c in calls)
    distinct_days = len(issued_days)

    # Effective N: design effect from temporal clustering
    # Simple design effect: 1 + (avg cluster size - 1) * autocorr
    # Approximate: group by day, compute variance of daily counts
    from collections import Counter
    day_counts = Counter(c[0] for c in calls)
    counts = list(day_counts.values())
    if len(counts) > 1:
        mean_c = sum(counts) / len(counts)
        var_c = sum((c - mean_c)**2 for c in counts) / (len(counts) - 1)
        deff = 1 + (var_c / mean_c) if mean_c > 0 else 1
    else:
        deff = 1
    deff = max(deff, 1.01)  # ensure > 1
    effective_n = issued / deff

    # Opportunities count
    opportunities = locals().get('opportunities', 0)

    # Check minimum observations
    if issued < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    conn.close()

if __name__ == "__main__":
    main()