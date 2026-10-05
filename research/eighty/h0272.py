import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict
import math

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day, tzinfo=timezone.utc).timestamp())

def compute_vol(closes):
    if len(closes) < 2:
        return None
    rets = [math.log(closes[i] / closes[i-1]) for i in range(1, len(closes))]
    return math.sqrt(sum(r*r for r in rets) / (len(rets) - 1)) * math.sqrt(252) if len(rets) > 1 else 0.0

def percentile_rank(arr, val):
    if not arr:
        return 0.5
    sorted_arr = sorted(arr)
    n = len(sorted_arr)
    count = sum(1 for x in sorted_arr if x <= val)
    return count / n

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all insider purchase filings (code='P')
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL
    """)
    raw_trades = cur.fetchall()
    if not raw_trades:
        print("INSUFFICIENT=1")
        return 0

    # Deduplicate by (symbol_id, filing_date)
    candidates = defaultdict(list)
    for row in raw_trades:
        d = ts_to_date(row['filed_ts'])
        candidates[row['symbol_id']].append(d)
    for sym in candidates:
        candidates[sym] = sorted(set(candidates[sym]))

    # Get symbols that have insider trades
    symbol_ids = list(candidates.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # Load daily bars for these symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_sym = defaultdict(list)
    for row in cur.fetchall():
        bars_by_sym[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

    # Load symbol info
    cur.execute(f"SELECT id, symbol, delisted_at FROM symbols WHERE id IN ({placeholders})", symbol_ids)
    sym_info = {row['id']: row for row in cur.fetchall()}

    opportunities = []
    issued_calls = []

    # For cross-sectional vol decile, we need all symbols' 20-session vol on each day
    # First, compute 20-session vol for all symbols on all days where possible
    vol_by_day_sym = defaultdict(dict)  # day_ts -> {symbol_id: vol}
    for sym_id, bars in bars_by_sym.items():
        if len(bars) < 20:
            continue
        closes = [b[1] for b in bars]
        for i in range(19, len(bars)):
            vol = compute_vol(closes[i-19:i+1])
            if vol is not None:
                day_ts = bars[i][0]
                vol_by_day_sym[day_ts][sym_id] = vol

    # Compute cross-sectional top decile threshold per day
    top_decile_thresh = {}
    for day_ts, vols in vol_by_day_sym.items():
        if len(vols) >= 10:
            sorted_vols = sorted(vols.values())
            idx = int(0.9 * len(sorted_vols))
            top_decile_thresh[day_ts] = sorted_vols[idx]
        else:
            top_decile_thresh[day_ts] = float('inf')

    # Track last call day per symbol (trading day index)
    last_call_idx = {}

    for sym_id, filing_dates in candidates.items():
        bars = bars_by_sym.get(sym_id, [])
        if len(bars) < 252 + 20:
            continue
        ts_list = [b[0] for b in bars]
        closes = [b[1] for b in bars]
        volumes = [b[2] for b in bars]

        # Map ts to index
        ts_to_idx = {ts: i for i, ts in enumerate(ts_list)}

        for filing_date in filing_dates:
            T_ts = date_to_ts(filing_date)
            if T_ts not in ts_to_idx:
                continue
            idx = ts_to_idx[T_ts]

            # Universe checks
            if idx < 252:
                continue
            close_T = closes[idx]
            if close_T < 5:
                continue
            # ADV over T-60..T-1 (60 days before T)
            if idx < 60:
                continue
            adv = sum(closes[i] * volumes[i] for i in range(idx-60, idx)) / 60
            if adv < 5_000_000:
                continue

            # This is an opportunity
            opportunities.append((sym_id, T_ts))

            # Entry condition (a): already satisfied by candidate selection (code='P' filing at T)
            # Entry condition (b): 20-session vol at T in bottom 40% of 252-day rolling distribution
            if idx < 251 + 19:
                continue
            vol_T = vol_by_day_sym.get(T_ts, {}).get(sym_id)
            if vol_T is None:
                continue

            # Compute 252-day rolling distribution of 20-session vol ending at T
            rolling_vols = []
            for j in range(idx - 251, idx + 1):
                day_ts_j = ts_list[j]
                v = vol_by_day_sym.get(day_ts_j, {}).get(sym_id)
                if v is not None:
                    rolling_vols.append(v)
            if len(rolling_vols) < 252:
                continue
            pct = percentile_rank(rolling_vols, vol_T)
            if pct > 0.4:
                continue

            # Abstain: 20-session vol at T in top cross-sectional decile
            if vol_T >= top_decile_thresh.get(T_ts, float('inf')):
                continue

            # Abstain: call issued for same symbol in prior 20 trading days
            last_idx = last_call_idx.get(sym_id, -1000)
            if idx - last_idx <= 20:
                continue

            # All conditions met - issue call
            issued_calls.append((sym_id, T_ts, idx))
            last_call_idx[sym_id] = idx

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # Get labels: T+10 trading days close-to-close
    hits = 0
    labels = []
    for sym_id, T_ts, idx in issued_calls:
        bars = bars_by_sym[sym_id]
        if idx + 10 >= len(bars):
            labels.append(None)
            continue
        close_T = bars[idx][1]
        close_T10 = bars[idx + 10][1]
        fwd_ret = (close_T10 - close_T) / close_T
        up = 1 if fwd_ret > 0 else 0
        labels.append(up)
        if up:
            hits += 1

    # Filter out calls with no label
    valid_calls = [(c, l) for c, l in zip(issued_calls, labels) if l is not None]
    if not valid_calls:
        print("INSUFFICIENT=1")
        return 0

    issued_calls = [c for c, _ in valid_calls]
    labels = [l for _, l in valid_calls]
    hits = sum(labels)
    issued = len(issued_calls)

    # Split by time: most recent 20% sealed
    issued_calls_sorted = sorted(issued_calls, key=lambda x: x[1])
    labels_sorted = [l for _, l in sorted(zip(issued_calls, labels), key=lambda x: x[0][1])]
    n_sealed = max(1, int(0.2 * issued))
    main_calls = issued_calls_sorted[:-n_sealed]
    main_labels = labels_sorted[:-n_sealed]
    sealed_calls = issued_calls_sorted[-n_sealed:]
    sealed_labels = labels_sorted[-n_sealed:]

    main_issued = len(main_calls)
    main_hits = sum(main_labels)
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(sealed_labels)

    precision = main_hits / main_issued if main_issued else 0.0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0
    base_rate = hits / issued if issued else 0.0

    # Distinct days among issued calls
    distinct_days = len(set(ts_to_date(c[1]) for c in issued_calls))

    # Design effect: Kish effective sample size based on day clusters
    day_counts = defaultdict(int)
    for c in issued_calls:
        day_counts[ts_to_date(c[1])] += 1
    m_values = list(day_counts.values())
    sum_m = sum(m_values)
    sum_m2 = sum(m*m for m in m_values)
    design_effect = sum_m2 / sum_m if sum_m else 1.0
    effective_n = issued / design_effect if design_effect > 0 else issued
    if effective_n >= issued:
        effective_n = issued - 1e-9

    opportunities_count = len(opportunities)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())