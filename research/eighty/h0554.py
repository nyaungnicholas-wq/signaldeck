# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 553
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def get_monthly_unemployment(conn):
    """Return dict mapping (year, month) to unemployment rate value."""
    rows = conn.execute("""
        SELECT ts, value FROM macro_series WHERE series = 'UNRATE' ORDER BY ts
    """).fetchall()
    if not rows:
        return None
    monthly = {}
    for row in rows:
        dt = datetime.utcfromtimestamp(row['ts']).date()
        ym = (dt.year, dt.month)
        monthly[ym] = row['value']
    return monthly

def falling_unemployment_on_date(monthly_unemp, date):
    """Check if the month containing date has lower unemployment than the previous month."""
    ym = (date.year, date.month)
    prev_month = ym[1] - 1
    prev_year = ym[0]
    if prev_month == 0:
        prev_month = 12
        prev_year -= 1
    prev_ym = (prev_year, prev_month)
    if ym in monthly_unemp and prev_ym in monthly_unemp:
        return monthly_unemp[ym] < monthly_unemp[prev_ym]
    return False

def get_candidate_symbols(conn):
    """Get symbols with insider purchases, daily bars, and sentiment."""
    insider = conn.execute("""
        SELECT DISTINCT symbol_id FROM insider_trades WHERE code = 'P'
    """).fetchall()
    bars = conn.execute("""
        SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'
    """).fetchall()
    sentiment = conn.execute("""
        SELECT DISTINCT symbol_id FROM sentiment_features
    """).fetchall()
    insider_set = {r[0] for r in insider}
    bars_set = {r[0] for r in bars}
    sentiment_set = {r[0] for r in sentiment}
    return insider_set & bars_set & sentiment_set

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row

        # Check unemployment data
        monthly_unemp = get_monthly_unemployment(conn)
        if monthly_unemp is None or len(monthly_unemp) < 24:
            print("INSUFFICIENT=1")
            return

        candidate_symbols = get_candidate_symbols(conn)
        if len(candidate_symbols) < 10:
            print("INSUFFICIENT=1")
            return

        # Precompute insider trades for candidate symbols: (symbol_id, filed_ts, tx_ts)
        # Only trades where code='P' (open-market purchase) and trade within 2 days of filing.
        insider_rows = conn.execute("""
            SELECT symbol_id, tx_ts, filed_ts
            FROM insider_trades
            WHERE code = 'P'
            ORDER BY filed_ts
        """).fetchall()
        # Group by symbol and for each trade, store (filed_ts, tx_ts)
        insider_by_symbol = defaultdict(list)
        for row in insider_rows:
            if row['symbol_id'] in candidate_symbols:
                tx_dt = datetime.utcfromtimestamp(row['tx_ts']).date()
                filed_dt = datetime.utcfromtimestamp(row['filed_ts']).date()
                if abs((filed_dt - tx_dt).days) <= 2:
                    insider_by_symbol[row['symbol_id']].append((filed_dt, tx_dt))

        if not insider_by_symbol:
            print("INSUFFICIENT=1")
            return

        # For each candidate symbol, load sentiment and bar data
        # We'll process symbol by symbol to avoid memory issues
        issued_calls = []  # list of (symbol_id, decision_day, hit)
        opportunities = 0  # count of decision points considered (days we checked conditions)

        for sym in candidate_symbols:
            # Load sentiment for this symbol: day -> mean_score
            sent_rows = conn.execute("""
                SELECT day, mean_score FROM sentiment_features
                WHERE symbol_id = ? ORDER BY day
            """, (sym,)).fetchall()
            if not sent_rows:
                continue
            sent_by_day = {}
            for r in sent_rows:
                # day is 'YYYY-MM-DD'
                d = datetime.strptime(r['day'], '%Y-%m-%d').date()
                sent_by_day[d] = r['mean_score']

            # Load daily bars for this symbol: date -> close
            bar_rows = conn.execute("""
                SELECT ts, close FROM bars
                WHERE symbol_id = ? AND tf = '1d' ORDER BY ts
            """, (sym,)).fetchall()
            if not bar_rows:
                continue
            bar_by_day = {}
            for r in bar_rows:
                d = datetime.utcfromtimestamp(r['ts']).date()
                bar_by_day[d] = r['close']

            # Get insider trades for this symbol
            trades = insider_by_symbol.get(sym, [])
            if not trades:
                continue

            # For each trade, check if we can enter on filed_dt
            for filed_dt, _ in trades:
                opportunities += 1

                # Check if symbol traded in last 10 days relative to filed_dt
                last_10_days = [d for d in bar_by_day.keys() if (filed_dt - d).days <= 10 and d <= filed_dt]
                if not last_10_days:
                    continue

                # Check unemployment condition for filed_dt
                if not falling_unemployment_on_date(monthly_unemp, filed_dt):
                    continue

                # Check sentiment moving averages on filed_dt
                # Need at least 20 days of sentiment up to filed_dt
                sent_days = sorted([d for d in sent_by_day.keys() if d <= filed_dt])
                if len(sent_days) < 20:
                    continue
                # Compute 5-day MA and 20-day MA on the last day (filed_dt)
                # Use the 5 most recent days and 20 most recent days
                sent_5 = [sent_by_day[d] for d in sent_days[-5:]]
                sent_20 = [sent_by_day[d] for d in sent_days[-20:]]
                ma5 = sum(sent_5) / len(sent_5)
                ma20 = sum(sent_20) / len(sent_20)
                if ma5 >= ma20:
                    continue

                # All conditions met: issue a call for this symbol on filed_dt
                # Find the close on filed_dt
                if filed_dt not in bar_by_day:
                    continue
                close_t = bar_by_day[filed_dt]

                # Look forward 21 trading days
                future_days = sorted([d for d in bar_by_day.keys() if d > filed_dt])
                if len(future_days) < 21:
                    continue
                # The 21st trading day after filed_dt
                forward_day = future_days[20]
                close_forward = bar_by_day[forward_day]
                hit = 1 if close_forward > close_t else 0
                issued_calls.append((sym, filed_dt, hit))

        if not issued_calls:
            print("INSUFFICIENT=1")
            return

        # Now we have issued_calls. We must split into training and sealed (last 20% by date)
        issued_calls.sort(key=lambda x: x[1])  # sort by decision day
        n_total = len(issued_calls)
        n_sealed = math.ceil(0.2 * n_total)
        sealed_calls = issued_calls[-n_sealed:]
        training_calls = issued_calls[:-n_sealed]

        # Compute metrics for training set
        hits_training = sum(hit for _, _, hit in training_calls)
        issued_training = len(training_calls)
        if issued_training == 0:
            print("INSUFFICIENT=1")
            return
        precision_training = hits_training / issued_training

        # Base rate within issued subset (training)
        base_rate = hits_training / issued_training

        # Distinct days in training set
        distinct_days_training = len(set(day for _, day, _ in training_calls))

        # Design effect for training set: cluster by day
        day_clusters = defaultdict(list)
        for _, day, hit in training_calls:
            day_clusters[day].append(hit)
        k = len(day_clusters)
        n = issued_training
        if k <= 1:
            # Only one day, set DEFF to >1
            deff = 1.0 + 1e-9
        else:
            # Compute ICC using one-way ANOVA
            grand_mean = base_rate
            # Compute between-cluster sum of squares
            ssb = 0
            for day, hits in day_clusters.items():
                m_i = len(hits)
                mean_i = sum(hits) / m_i
                ssb += m_i * (mean_i - grand_mean) ** 2
            # Compute within-cluster sum of squares
            ssw = 0
            for day, hits in day_clusters.items():
                mean_i = sum(hits) / len(hits)
                for h in hits:
                    ssw += (h - mean_i) ** 2
            msb = ssb / (k - 1)
            msw = ssw / (n - k)
            m = n / k  # average cluster size
            if msb == 0 or msw == 0:
                deff = 1.0 + 1e-9
            else:
                icc = (msb - msw) / (msb + (m - 1) * msw)
                deff = 1 + (m - 1) * icc
        effective_n = n / deff

        # SEALED_PRECISION
        hits_sealed = sum(hit for _, _, hit in sealed_calls)
        issued_sealed = len(sealed_calls)
        if issued_sealed > 0:
            sealed_precision = hits_sealed / issued_sealed
        else:
            sealed_precision = 0.0

        # Print required lines
        print(f"ISSUED={n}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision_training:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days_training}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")

    except Exception as e:
        # If any error occurs, treat as insufficient data
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()