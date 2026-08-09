# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 464
# cycle_index: 55
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timezone

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1) Load open-market insider purchases (code P) with filed_ts (knowable at decision time)
    cur.execute("""
        SELECT symbol_id, filed_ts, insider
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    insider_rows = cur.fetchall()
    if not insider_rows:
        print("INSUFFICIENT=1")
        return

    # 2) Load daily news volume (n_all) from sentiment_features
    cur.execute("SELECT symbol_id, day, n_all FROM sentiment_features ORDER BY symbol_id, day")
    sf_rows = cur.fetchall()
    if not sf_rows:
        print("INSUFFICIENT=1")
        return

    # Build per-symbol time series of n_all keyed by day (YYYY-MM-DD)
    sf_by_sym = defaultdict(list)
    for r in sf_rows:
        sf_by_sym[r['symbol_id']].append((r['day'], r['n_all']))

    # Compute 20-day rolling median per symbol
    sf_median = {}
    for sym, series in sf_by_sym.items():
        series.sort(key=lambda x: x[0])
        days = [d for d, _ in series]
        vals = [v for _, v in series]
        medians = {}
        for i in range(len(vals)):
            window = vals[max(0, i-19):i+1]
            if len(window) >= 5:  # require at least 5 days in window
                medians[days[i]] = sorted(window)[len(window)//2]
        sf_median[sym] = medians

    # 3) Qualify insider trades: news volume > 2x 20-day median on filing day
    qualified = []  # (symbol_id, filed_ts, insider, day_str)
    for r in insider_rows:
        sym = r['symbol_id']
        filed_ts = r['filed_ts']
        day_str = datetime.fromtimestamp(filed_ts, tz=timezone.utc).strftime('%Y-%m-%d')
        med = sf_median.get(sym, {}).get(day_str)
        n_all = next((v for d, v in sf_by_sym.get(sym, []) if d == day_str), None)
        if med and n_all and n_all > 2 * med:
            qualified.append((sym, filed_ts, r['insider'], day_str))

    if not qualified:
        print("INSUFFICIENT=1")
        return

    # 4) Abstain: no other insider purchase for same symbol in prior 20 days
    # Build per-symbol list of purchase days
    purch_days = defaultdict(list)
    for sym, ts, insider, day in qualified:
        purch_days[sym].append((ts, day, insider))
    for sym in purch_days:
        purch_days[sym].sort(key=lambda x: x[0])

    filtered = []
    for sym, events in purch_days.items():
        for i, (ts, day, insider) in enumerate(events):
            # check prior 20 days
            ok = True
            for j in range(i-1, -1, -1):
                ts2, _, _ = events[j]
                if ts - ts2 <= 20 * 86400:
                    ok = False
                    break
                if ts - ts2 > 20 * 86400:
                    break
            if ok:
                filtered.append((sym, ts, insider, day))

    # 5) Abstain: at least 2 distinct insiders buying in the same quarter
    qtr_insiders = defaultdict(set)
    for sym, ts, insider, day in filtered:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        qtr = (dt.year, (dt.month-1)//3)
        qtr_insiders[(sym, qtr)].add(insider)

    final_candidates = []
    for sym, ts, insider, day in filtered:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        qtr = (dt.year, (dt.month-1)//3)
        if len(qtr_insiders[(sym, qtr)]) >= 2:
            final_candidates.append((sym, ts, day))

    if not final_candidates:
        print("INSUFFICIENT=1")
        return

    # 6) Load labels from prediction_outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    label_rows = cur.fetchall()
    if not label_rows:
        print("INSUFFICIENT=1")
        return

    # Build latest label as of each decision timestamp per symbol
    labels_by_sym = defaultdict(list)
    for r in label_rows:
        labels_by_sym[r['symbol_id']].append((r['ts'], r['up'], r['fwd_return']))
    for sym in labels_by_sym:
        labels_by_sym[sym].sort(key=lambda x: x[0])

    # 7) Match each candidate to latest label with label.ts <= filed_ts
    observations = []  # (symbol_id, decision_ts, day_str, up, fwd_return)
    for sym, ts, day in final_candidates:
        labs = labels_by_sym.get(sym, [])
        # binary search for latest label.ts <= ts
        lo, hi = 0, len(labs)
        while lo < hi:
            mid = (lo + hi) // 2
            if labs[mid][0] <= ts:
                lo = mid + 1
            else:
                hi = mid
        idx = lo - 1
        if idx >= 0:
            _, up, fwd = labs[idx]
            observations.append((sym, ts, day, up, fwd))

    if not observations:
        print("INSUFFICIENT=1")
        return

    # 8) Deduplicate to one observation per (symbol, UTC day)
    obs_by_day = {}
    for sym, ts, day, up, fwd in observations:
        key = (sym, day)
        if key not in obs_by_day:
            obs_by_day[key] = (sym, ts, day, up, fwd)
    deduped = list(obs_by_day.values())

    # 9) Time-based split: most recent 20% of decision days as sealed era
    deduped.sort(key=lambda x: x[1])  # sort by decision_ts
    decision_days = sorted(set(o[2] for o in deduped))  # unique day strings
    split_idx = max(1, int(len(decision_days) * 0.8))
    train_days = set(decision_days[:split_idx])
    sealed_days = set(decision_days[split_idx:])

    train_obs = [o for o in deduped if o[2] in train_days]
    sealed_obs = [o for o in deduped if o[2] in sealed_days]

    def compute_metrics(obs_list):
        if not obs_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(obs_list)
        hits = sum(1 for o in obs_list if o[3] == 1)
        precision = hits / issued
        base_rate = hits / issued  # base rate within issued subset = precision for binary up
        distinct_days = len(set(o[2] for o in obs_list))
        # Design effect approximation: 1 + (avg cluster size - 1) * rho
        # Cluster by day: count obs per day
        day_counts = defaultdict(int)
        for o in obs_list:
            day_counts[o[2]] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        rho = 0.1  # conservative intra-day correlation estimate
        deff = 1 + (avg_cluster - 1) * rho
        effective_n = issued / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    tr_issued, tr_hits, tr_prec, tr_base, tr_days, tr_eff = compute_metrics(train_obs)
    sl_issued, sl_hits, sl_prec, sl_base, sl_days, sl_eff = compute_metrics(sealed_obs)

    if tr_issued < 30 or tr_days < 10:
        print("INSUFFICIENT=1")
        return

    # 10) Print required metrics (training era)
    print(f"ISSUED={tr_issued}")
    print(f"OPPORTUNITIES={len(final_candidates)}")
    print(f"PRECISION={tr_prec:.6f}")
    print(f"BASE_RATE={tr_base:.6f}")
    print(f"DISTINCT_DAYS={tr_days}")
    print(f"EFFECTIVE_N={tr_eff:.2f}")
    print(f"SEALED_PRECISION={sl_prec:.6f}")

if __name__ == '__main__':
    main()