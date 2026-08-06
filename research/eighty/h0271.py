import sqlite3
import math
from datetime import datetime, timedelta
from collections import defaultdict
from statistics import mean, stdev
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get symbols with insider purchases (code='P')
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades WHERE code = 'P'")
    symbol_ids = [row['symbol_id'] for row in cur.fetchall()]
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return

    placeholders = ','.join('?' * len(symbol_ids))

    # Load daily bars for these symbols (tf='1d')
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        dt = datetime.utcfromtimestamp(row['ts']).date()
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'date': dt,
            'close': row['close'],
            'volume': row['volume'],
        })

    # Load insider purchases with filed_ts (disclosure date)
    cur.execute(f"""
        SELECT symbol_id, insider, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, filed_ts
    """, symbol_ids)
    insider_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        filed_date = datetime.utcfromtimestamp(row['filed_ts']).date()
        insider_by_symbol[row['symbol_id']].append({
            'insider': row['insider'],
            'filed_date': filed_date,
        })

    conn.close()

    # Precompute features per symbol
    all_candidates = []
    vol_20_by_date = defaultdict(list)

    for sym_id, bars in bars_by_symbol.items():
        n = len(bars)
        if n < 280:  # need 252 prior + 20 vol window + 10 forward
            continue

        closes = [b['close'] for b in bars]
        volumes = [b['volume'] for b in bars]
        dates = [b['date'] for b in bars]
        timestamps = [b['ts'] for b in bars]

        # Log returns
        log_rets = [0.0] * n
        for i in range(1, n):
            if closes[i-1] > 0:
                log_rets[i] = math.log(closes[i] / closes[i-1])

        # 20-session realized volatility (std of last 20 log returns)
        vol_20 = [None] * n
        for i in range(19, n):
            window = log_rets[i-19:i+1]
            if len(window) == 20:
                vol_20[i] = stdev(window) if len(set(window)) > 1 else 0.0

        # 60-day ADV (dollar volume) - average over T-60..T-1
        adv_60 = [None] * n
        for i in range(60, n):
            dollar_vols = [closes[j] * volumes[j] for j in range(i-60, i)]
            adv_60[i] = mean(dollar_vols)

        # Forward return T+10 (close-to-close)
        fwd_ret_10 = [None] * n
        for i in range(n - 10):
            if closes[i] > 0:
                fwd_ret_10[i] = closes[i+10] / closes[i] - 1.0

        # Collect candidates meeting universe criteria
        for i in range(270, n - 10):  # 252 prior + 19 for vol_20 window = 271 min, but 270 is safe
            if vol_20[i] is None or adv_60[i] is None or fwd_ret_10[i] is None:
                continue
            if closes[i] < 5.0:
                continue
            if adv_60[i] < 5_000_000:
                continue

            all_candidates.append({
                'symbol_id': sym_id,
                'bar_idx': i,
                'ts': timestamps[i],
                'date': dates[i],
                'close': closes[i],
                'vol_20': vol_20[i],
                'adv_60': adv_60[i],
                'fwd_ret_10': fwd_ret_10[i],
                'vol_20_series': vol_20,
                'dates': dates,
                'closes': closes,
            })
            vol_20_by_date[dates[i]].append((sym_id, vol_20[i]))

    if not all_candidates:
        print("INSUFFICIENT=1")
        return

    # Cross-sectional top decile of vol_20 per date (abstain if in top decile)
    top_decile_threshold = {}
    for date, vols in vol_20_by_date.items():
        if len(vols) >= 10:
            vals = [v for _, v in vols]
            vals.sort()
            idx = int(len(vals) * 0.9)
            top_decile_threshold[date] = vals[idx]
        else:
            top_decile_threshold[date] = float('inf')

    # Insider lookup: for each symbol, map date -> set of insiders who filed purchases
    insider_filings_by_sym_date = defaultdict(lambda: defaultdict(set))
    for sym_id, filings in insider_by_symbol.items():
        for f in filings:
            insider_filings_by_sym_date[sym_id][f['filed_date']].add(f['insider'])

    # Evaluate each candidate
    issued_calls = []  # (date, symbol_id, ts, fwd_ret_10, hit)
    last_call_date_by_symbol = {}  # symbol_id -> last call date (for 20-day cooldown)

    for cand in all_candidates:
        sym_id = cand['symbol_id']
        date = cand['date']
        ts = cand['ts']
        vol_20 = cand['vol_20']
        fwd_ret_10 = cand['fwd_ret_10']
        vol_20_series = cand['vol_20_series']
        dates = cand['dates']
        bar_idx = cand['bar_idx']

        # ABSTENTION: cross-sectional top decile
        if date in top_decile_threshold and vol_20 > top_decile_threshold[date]:
            continue

        # ABSTENTION: 20-session vol in bottom 50% of 252-day rolling distribution
        # Need 252 prior vol_20 values (indices bar_idx-252 .. bar_idx-1)
        start_idx = bar_idx - 252
        if start_idx < 0:
            continue
        rolling_vols = [v for v in vol_20_series[start_idx:bar_idx] if v is not None]
        if len(rolling_vols) < 126:  # need sufficient history
            continue
        rolling_vols.sort()
        median_vol = rolling_vols[len(rolling_vols) // 2]
        if vol_20 > median_vol:  # not in bottom 50%
            continue

        # ENTRY: two or more distinct insiders filed purchases in T-5..T (inclusive)
        # filed_date in [date-5, date]
        insider_set = set()
        for d in range(-5, 1):
            check_date = date - timedelta(days=d)
            insider_set.update(insider_filings_by_sym_date[sym_id].get(check_date, set()))
        if len(insider_set) < 2:
            continue

        # ABSTENTION: no call for same symbol in prior 20 trading days
        last_call = last_call_date_by_symbol.get(sym_id)
        if last_call is not None:
            # Count trading days between last_call and date
            # Approximate: find indices in dates list
            try:
                last_idx = dates.index(last_call)
                curr_idx = dates.index(date)
                if curr_idx - last_idx < 20:
                    continue
            except ValueError:
                pass

        # All conditions met - issue call
        hit = 1 if fwd_ret_10 > 0 else 0
        issued_calls.append({
            'date': date,
            'symbol_id': sym_id,
            'ts': ts,
            'fwd_ret_10': fwd_ret_10,
            'hit': hit,
        })
        last_call_date_by_symbol[sym_id] = date

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Sort by timestamp
    issued_calls.sort(key=lambda x: x['ts'])

    # Hold out most recent 20% as sealed era
    n_issued = len(issued_calls)
    split_idx = int(n_issued * 0.8)
    main_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]

    # Opportunities = all candidate decision points considered (universe-filtered)
    opportunities = len(all_candidates)

    # Main era metrics
    issued_main = len(main_calls)
    hits_main = sum(c['hit'] for c in main_calls)
    precision_main = hits_main / issued_main if issued_main > 0 else 0.0

    # Base rate within issued subset (main era)
    base_rate_main = hits_main / issued_main if issued_main > 0 else 0.0

    # Distinct days among issued calls (main era)
    distinct_days_main = len(set(c['date'] for c in main_calls))

    # Design effect for EFFECTIVE_N
    # Cluster by date: count calls per day
    calls_per_day = defaultdict(int)
    for c in main_calls:
        calls_per_day[c['date']] += 1
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Conservative: assume ICC=1 for same-day calls (perfect correlation within day)
    # Effective N = sum over days of 1 = number of distinct days
    # But more precisely: design_effect = (sum n_d^2) / (sum n_d) where n_d = calls on day d
    # Effective N = issued / design_effect
    total_calls = issued_main
    sum_sq = sum(n * n for n in calls_per_day.values())
    design_effect = sum_sq / total_calls if total_calls > 0 else 1.0
    effective_n = total_calls / design_effect if design_effect > 0 else 0.0

    # Sealed era metrics
    issued_sealed = len(sealed_calls)
    hits_sealed = sum(c['hit'] for c in sealed_calls)
    sealed_precision = hits_sealed / issued_sealed if issued_sealed > 0 else 0.0

    # Check minimum observations
    if issued_main < 30:
        print("INSUFFICIENT=1")
        return

    # Print required lines
    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_main:.6f}")
    print(f"BASE_RATE={base_rate_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_main}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()