import sqlite3
from datetime import datetime, timezone

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get DGS10 data sorted by timestamp
    cur.execute("SELECT ts, value FROM macro_series WHERE series='DGS10' ORDER BY ts")
    dgs10 = [(row['ts'], row['value']) for row in cur.fetchall()]
    if len(dgs10) < 3:
        print("INSUFFICIENT=1")
        return

    # Convert to date-based structure (unix epoch -> date)
    dgs10_dates = []
    for ts, val in dgs10:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc).date()
        dgs10_dates.append((dt, val))

    # Find signal dates where jump >= 0.10 pp between T-1 and T-2
    signal_dates = []
    for i in range(2, len(dgs10_dates)):
        dt_t1, val_t1 = dgs10_dates[i-1]
        dt_t2, val_t2 = dgs10_dates[i-2]
        jump = val_t1 - val_t2
        if jump >= 0.10:
            # Signal date T is the next business day after T-1
            dt_t0 = dgs10_dates[i][0]  # This is T
            signal_dates.append((dt_t0, jump))

    if not signal_dates:
        print("INSUFFICIENT=1")
        return

    # 2. Get all daily bars for fast lookup
    cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d'")
    bars = {}
    for row in cur.fetchall():
        symbol_id = row['symbol_id']
        dt = datetime.fromtimestamp(row['ts'], tz=timezone.utc).date()
        if symbol_id not in bars:
            bars[symbol_id] = {}
        bars[symbol_id][dt] = row['close']

    # 3. Get EPS data (fundamentals with metric='EPS')
    cur.execute("SELECT symbol_id, value, as_of, fetched_at FROM fundamentals WHERE metric='EPS' ORDER BY fetched_at")
    eps_data = {}
    for row in cur.fetchall():
        symbol_id = row['symbol_id']
        eps = row['value']
        fetched_dt = datetime.fromtimestamp(row['fetched_at'], tz=timezone.utc).date()
        if symbol_id not in eps_data:
            eps_data[symbol_id] = []
        eps_data[symbol_id].append((fetched_dt, eps))

    # For each symbol, sort by fetched_dt descending to find latest known EPS as of date
    for symbol_id in eps_data:
        eps_data[symbol_id].sort(key=lambda x: x[0], reverse=True)

    # 4. Process each signal date
    issued = []
    opportunities = 0
    
    for signal_dt, jump in signal_dates:
        # For each symbol in bars, check eligibility
        for symbol_id, symbol_bars in bars.items():
            # Check if symbol has a bar on signal date T
            if signal_dt not in symbol_bars:
                continue
            opportunities += 1
            
            # Get latest known EPS as of T (fetched_at <= signal_dt)
            if symbol_id not in eps_data:
                continue
            eps_list = eps_data[symbol_id]
            latest_eps = None
            for fetched_dt, eps in eps_list:
                if fetched_dt <= signal_dt:
                    latest_eps = eps
                    break
            if latest_eps is None or latest_eps > 0:
                continue
            
            # Find close at T+5 trading days
            # Convert symbol_bars to sorted list of dates
            sym_dates = sorted(symbol_bars.keys())
            if signal_dt not in sym_dates:
                continue
            idx = sym_dates.index(signal_dt)
            if idx + 5 >= len(sym_dates):
                continue
            t5_dt = sym_dates[idx + 5]
            close_t = symbol_bars[signal_dt]
            close_t5 = symbol_bars[t5_dt]
            if close_t5 < close_t:
                outcome = 1  # DOWN realized
            else:
                outcome = 0
            
            issued.append((signal_dt, symbol_id, outcome))

    if not issued:
        print("INSUFFICIENT=1")
        return

    # 5. Split into in-sample (80%) and sealed (20%) by date
    all_dates = sorted(set(dt for dt, _, _ in issued))
    split_idx = int(len(all_dates) * 0.8)
    in_sample_dates = all_dates[:split_idx]
    sealed_dates = all_dates[split_idx:]

    # Filter calls
    in_sample = [(dt, sid, out) for dt, sid, out in issued if dt in in_sample_dates]
    sealed = [(dt, sid, out) for dt, sid, out in issued if dt in sealed_dates]

    if not in_sample:
        print("INSUFFICIENT=1")
        return

    # 6. Compute metrics
    hits_in = sum(out for _, _, out in in_sample)
    issued_in = len(in_sample)
    precision_in = hits_in / issued_in if issued_in > 0 else 0
    
    # Base rate: proportion of DOWN outcomes in issued subset (same as precision for all-DOWN calls)
    base_rate = hits_in / issued_in if issued_in > 0 else 0
    
    # Distinct days in issued calls (in-sample)
    distinct_days_in = len(set(dt for dt, _, _ in in_sample))
    
    # Compute design effect for clustering by day
    from collections import Counter
    day_counts = Counter(dt for dt, _, _ in in_sample)
    m_bar = issued_in / len(day_counts) if day_counts else 1
    
    # Intra-class correlation (ICC) for binary outcomes
    grand_mean = hits_in / issued_in
    msb = 0
    for dt, cnt in day_counts.items():
        day_hits = sum(out for d, _, out in in_sample if d == dt)
        day_mean = day_hits / cnt
        msb += cnt * (day_mean - grand_mean) ** 2
    msb /= (len(day_counts) - 1) if len(day_counts) > 1 else 1
    
    msw = 0
    for dt, cnt in day_counts.items():
        day_hits = sum(out for d, _, out in in_sample if d == dt)
        day_mean = day_hits / cnt
        for d, _, out in in_sample:
            if d == dt:
                msw += (out - day_mean) ** 2
    msw /= (issued_in - len(day_counts)) if issued_in > len(day_counts) else 1
    
    icc = (msb - msw) / (msb + (m_bar - 1) * msw) if (msb + (m_bar - 1) * msw) > 0 else 0
    design_effect = 1 + (m_bar - 1) * icc
    effective_n = issued_in / design_effect
    
    # Sealed metrics
    if sealed:
        hits_sealed = sum(out for _, _, out in sealed)
        issued_sealed = len(sealed)
        precision_sealed = hits_sealed / issued_sealed
    else:
        precision_sealed = 0

    # 7. Output required lines
    print(f"ISSUED={issued_in}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_in:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_in}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")

    conn.close()

if __name__ == "__main__":
    main()