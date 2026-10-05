# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 404
# cycle_index: 72
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row

    # 1. Verify horizon=21 exists in prediction_outcomes
    cur = conn.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE horizon=21")
    if cur.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return

    # 2. Load all horizon=21 outcomes ordered by decision timestamp
    outcomes = conn.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon=21
        ORDER BY ts
    """).fetchall()

    if len(outcomes) < 200:
        print("INSUFFICIENT=1")
        return

    # 3. Split 80/20 by timestamp (sealed = most recent 20%)
    split_idx = int(len(outcomes) * 0.8)
    main_outcomes = outcomes[:split_idx]
    sealed_outcomes = outcomes[split_idx:]

    # 4. Pre-load StockTwits daily totals per symbol for fast percentile calc
    st_rows = conn.execute("""
        SELECT symbol_id, ts, total
        FROM stocktwits_sentiment
        WHERE total > 0
    """).fetchall()

    st_by_symbol = defaultdict(list)
    for r in st_rows:
        d = unix_to_date(r['ts'])
        st_by_symbol[r['symbol_id']].append((d, r['total']))

    # Compute per-symbol daily medians over trailing 1 year (for each decision day)
    st_median_cache = {}

    def get_st_median(symbol_id, as_of_date):
        key = (symbol_id, as_of_date)
        if key in st_median_cache:
            return st_median_cache[key]
        history = st_by_symbol.get(symbol_id, [])
        if not history:
            st_median_cache[key] = None
            return None
        cutoff = as_of_date - timedelta(days=365)
        vals = [v for d, v in history if cutoff <= d < as_of_date]
        if len(vals) < 20:
            st_median_cache[key] = None
            return None
        vals.sort()
        med = vals[len(vals)//2]
        st_median_cache[key] = med
        return med

    # 5. Pre-load fundamentals Revenue by symbol_id, fetched_at
    rev_rows = conn.execute("""
        SELECT symbol_id, value, fetched_at
        FROM fundamentals
        WHERE metric='Revenues' AND value IS NOT NULL
        ORDER BY symbol_id, fetched_at
    """).fetchall()

    rev_by_symbol = defaultdict(list)
    for r in rev_rows:
        fa = r['fetched_at']
        if isinstance(fa, str) and '-' in fa:
            fa_ts = int(datetime.fromisoformat(fa).timestamp())
        else:
            fa_ts = int(fa)
        rev_by_symbol[r['symbol_id']].append((fa_ts, r['value']))

    def get_revenue_growth(symbol_id, as_of_ts):
        hist = rev_by_symbol.get(symbol_id, [])
        if len(hist) < 2:
            return False
        avail = [(ts, val) for ts, val in hist if ts <= as_of_ts]
        if len(avail) < 2:
            return False
        avail.sort(key=lambda x: x[0], reverse=True)
        latest_val = avail[0][1]
        prev_val = avail[1][1]
        if prev_val <= 0:
            return False
        return (latest_val - prev_val) / prev_val > 0

    # 6. Insider purchases lookup
    def check_insider_buys(symbol_ids, decision_timestamps):
        if not symbol_ids:
            return set()
        placeholders = ','.join('?' * len(symbol_ids))
        sym_list = list(symbol_ids)
        q = f"""
            SELECT symbol_id, filed_ts
            FROM insider_trades
            WHERE symbol_id IN ({placeholders}) AND code='P'
        """
        rows = conn.execute(q, sym_list).fetchall()
        buys_by_sym = defaultdict(list)
        for r in rows:
            buys_by_sym[r['symbol_id']].append(r['filed_ts'])

        result = set()
        for sym, ts in zip(symbol_ids, decision_timestamps):
            for ft in buys_by_sym.get(sym, []):
                if ts - 30*86400 <= ft <= ts:
                    result.add((sym, ts))
                    break
        return result

    # 7. Evaluate an era
    def evaluate_era(outcomes_list, era_name):
        if not outcomes_list:
            return 0, 0, 0, 0, 0, 0.0

        symbol_ids = [o['symbol_id'] for o in outcomes_list]
        decision_ts = [o['ts'] for o in outcomes_list]
        labels = [o['up'] for o in outcomes_list]

        insider_set = check_insider_buys(set(symbol_ids), decision_ts)

        issued = 0
        hits = 0
        issued_days = set()
        base_rate_pos = 0

        for i, (sym, ts, label) in enumerate(zip(symbol_ids, decision_ts, labels)):
            if (sym, ts) not in insider_set:
                continue

            if not get_revenue_growth(sym, ts):
                continue

            as_of_date = unix_to_date(ts) - timedelta(days=1)
            median = get_st_median(sym, as_of_date)
            if median is None:
                continue
            history = st_by_symbol.get(sym, [])
            cutoff = as_of_date - timedelta(days=20)
            recent_vals = [v for d, v in history if cutoff <= d <= as_of_date]
            if len(recent_vals) < 10:
                continue
            avg_20 = sum(recent_vals) / len(recent_vals)
            if avg_20 >= median:
                continue

            issued += 1
            issued_days.add(as_of_date)
            if label == 1:
                hits += 1
            if label == 1:
                base_rate_pos += 1

        precision = hits / issued if issued > 0 else 0.0
        base_rate = base_rate_pos / issued if issued > 0 else 0.0
        distinct_days = len(issued_days)

        # Design effect: cluster by day, compute variance inflation
        if issued > 1 and distinct_days > 0:
            # Count calls per day
            day_counts = defaultdict(int)
            for sym, ts, label in zip(symbol_ids, decision_ts, labels):
                if (sym, ts) in insider_set and get_revenue_growth(sym, ts):
                    as_of_date = unix_to_date(ts) - timedelta(days=1)
                    median = get_st_median(sym, as_of_date)
                    if median is None:
                        continue
                    history = st_by_symbol.get(sym, [])
                    cutoff = as_of_date - timedelta(days=20)
                    recent_vals = [v for d, v in history if cutoff <= d <= as_of_date]
                    if len(recent_vals) < 10:
                        continue
                    avg_20 = sum(recent_vals) / len(recent_vals)
                    if avg_20 >= median:
                        continue
                    day_counts[as_of_date] += 1
            
            if day_counts:
                avg_cluster = sum(day_counts.values()) / len(day_counts)
                # Design effect = 1 + (avg_cluster - 1) * ICC, approximate ICC=0.2
                deff = 1 + (avg_cluster - 1) * 0.2
                effective_n = issued / deff
            else:
                effective_n = issued * 0.5
        else:
            effective_n = issued * 0.5

        return issued, hits, distinct_days, base_rate, effective_n, precision

    # Evaluate main era
    main_issued, main_hits, main_days, main_base, main_eff_n, main_prec = evaluate_era(main_outcomes, "main")
    
    # Evaluate sealed era
    sealed_issued, sealed_hits, sealed_days, sealed_base, sealed_eff_n, sealed_prec = evaluate_era(sealed_outcomes, "sealed")

    # Total issued across both eras for reporting
    total_issued = main_issued + sealed_issued
    total_opportunities = len(main_outcomes) + len(sealed_outcomes)

    if total_issued == 0:
        print("INSUFFICIENT=1")
        return

    # Overall precision and base rate (weighted)
    total_hits = main_hits + sealed_hits
    overall_precision = total_hits / total_issued if total_issued > 0 else 0.0
    overall_base = (main_base * main_issued + sealed_base * sealed_issued) / total_issued if total_issued > 0 else 0.0
    overall_days = main_days + sealed_days
    overall_eff_n = main_eff_n + sealed_eff_n

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={overall_precision:.6f}")
    print(f"BASE_RATE={overall_base:.6f}")
    print(f"DISTINCT_DAYS={overall_days}")
    print(f"EFFECTIVE_N={overall_eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == "__main__":
    main()