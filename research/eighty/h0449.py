# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 448
# cycle_index: 39
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

    # 1. Build trading day calendar from 1d bars
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    trading_days_ts = [row['ts'] for row in cur.fetchall()]
    if not trading_days_ts:
        print("INSUFFICIENT=1")
        return 0
    trading_days = [datetime.utcfromtimestamp(ts).date() for ts in trading_days_ts]
    trading_day_set = set(trading_days)
    ts_to_date = {ts: datetime.utcfromtimestamp(ts).date() for ts in trading_days_ts}
    date_to_ts = {datetime.utcfromtimestamp(ts).date(): ts for ts in trading_days_ts}

    # 2. Find initial unemployment claims series in macro_series
    cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%claim%' OR series LIKE '%unemploy%' OR series LIKE '%ICSA%' OR series LIKE '%ICNSA%'")
    claim_series = [row['series'] for row in cur.fetchall()]
    if not claim_series:
        print("INSUFFICIENT=1")
        return 0

    # Get macro data for the first matching series (prefer ICSA)
    target_series = None
    for s in ['ICSA', 'ICNSA'] + claim_series:
        if s in claim_series:
            target_series = s
            break
    if not target_series:
        target_series = claim_series[0]

    cur.execute("SELECT ts, value FROM macro_series WHERE series=? ORDER BY ts", (target_series,))
    macro_rows = cur.fetchall()
    if len(macro_rows) < 60:
        print("INSUFFICIENT=1")
        return 0

    macro_dates = [datetime.utcfromtimestamp(row['ts']).date() for row in macro_rows]
    macro_values = [row['value'] for row in macro_rows]
    macro_dict = dict(zip(macro_dates, macro_values))

    # Compute 20-day MA and 5% decline from 40 days ago for each trading day
    # Macro data is daily (calendar), need to align to trading days
    macro_ma20 = {}
    macro_decline_ok = {}
    for i, d in enumerate(macro_dates):
        if i < 20:
            continue
        ma20 = sum(macro_values[i-19:i+1]) / 20
        macro_ma20[d] = ma20
        if i >= 40:
            ma20_40d_ago = sum(macro_values[i-59:i-39]) / 20
            if ma20_40d_ago > 0:
                decline = (ma20_40d_ago - ma20) / ma20_40d_ago
                macro_decline_ok[d] = decline >= 0.05
            else:
                macro_decline_ok[d] = False

    # Map to trading days: for each trading day, use latest available macro data on or before that day
    macro_ma20_td = {}
    macro_decline_td = {}
    macro_dates_sorted = sorted(macro_ma20.keys())
    for td in trading_days:
        # Find latest macro date <= td
        latest = None
        for md in reversed(macro_dates_sorted):
            if md <= td:
                latest = md
                break
        if latest:
            macro_ma20_td[td] = macro_ma20[latest]
            macro_decline_td[td] = macro_decline_ok.get(latest, False)

    # 3. Get insider open-market purchases (code='P') using filed_ts (not tx_ts)
    cur.execute("""
        SELECT symbol_id, filed_ts FROM insider_trades
        WHERE code='P' AND filed_ts IS NOT NULL
        ORDER BY symbol_id, filed_ts
    """)
    insider_rows = cur.fetchall()
    if not insider_rows:
        print("INSUFFICIENT=1")
        return 0

    # Group by symbol_id
    insider_by_symbol = defaultdict(list)
    for row in insider_rows:
        d = datetime.utcfromtimestamp(row['filed_ts']).date()
        insider_by_symbol[row['symbol_id']].append(d)

    # 4. Get sentiment_features (daily mean_score) for each symbol
    cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day")
    sent_rows = cur.fetchall()
    if not sent_rows:
        print("INSUFFICIENT=1")
        return 0

    sent_by_symbol = defaultdict(list)
    for row in sent_rows:
        d = datetime.strptime(row['day'], '%Y-%m-%d').date()
        sent_by_symbol[row['symbol_id']].append((d, row['mean_score']))

    # Compute 20-day and 200-day MA for each symbol
    sent_ma20 = defaultdict(dict)
    sent_ma200 = defaultdict(dict)
    for sym, data in sent_by_symbol.items():
        data.sort(key=lambda x: x[0])
        dates = [d for d, _ in data]
        scores = [s for _, s in data]
        for i in range(len(data)):
            if i >= 19:
                ma20 = sum(scores[i-19:i+1]) / 20
                sent_ma20[sym][dates[i]] = ma20
            if i >= 199:
                ma200 = sum(scores[i-199:i+1]) / 200
                sent_ma200[sym][dates[i]] = ma200

    # 5. Get prediction_outcomes for horizon=21 (check what horizon values exist)
    cur.execute("SELECT DISTINCT horizon FROM prediction_outcomes")
    horizons = [row['horizon'] for row in cur.fetchall()]
    # Horizon might be '21d', 21, '21', etc. Find closest to 21 days
    target_horizon = None
    for h in horizons:
        if str(h) in ('21', '21d', '21D'):
            target_horizon = h
            break
    if target_horizon is None:
        # Try to find numeric 21
        for h in horizons:
            try:
                if int(h) == 21:
                    target_horizon = h
                    break
            except:
                pass
    if target_horizon is None:
        print("INSUFFICIENT=1")
        return 0

    cur.execute("""
        SELECT symbol_id, ts, up FROM prediction_outcomes
        WHERE horizon=? AND up IS NOT NULL
    """, (target_horizon,))
    label_rows = cur.fetchall()
    if not label_rows:
        print("INSUFFICIENT=1")
        return 0

    labels = {}
    for row in label_rows:
        d = datetime.utcfromtimestamp(row['ts']).date()
        labels[(row['symbol_id'], d)] = row['up']

    # 6. Determine universe: symbols with at least one insider purchase in last 252 trading days
    # For each decision point (trading day), we need to know if symbol qualifies
    # We'll evaluate each trading day for each symbol that has insider data

    # Get all symbols that ever had insider purchase
    all_symbols_with_insider = set(insider_by_symbol.keys())

    # 7. Evaluate decision points
    # For each trading day, for each symbol in universe, check conditions
    opportunities = []
    issued_calls = []

    # Precompute for each symbol the list of insider purchase dates (filed_ts)
    for sym in all_symbols_with_insider:
        purchase_dates = sorted(insider_by_symbol[sym])
        if not purchase_dates:
            continue

        sent_data = sent_by_symbol.get(sym, [])
        if not sent_data:
            continue
        sent_dates = [d for d, _ in sent_data]

        # For each trading day where we have sentiment data up to that day
        for td in trading_days:
            # Universe check: at least one insider purchase in last 252 trading days
            # Find trading day index
            td_idx = trading_days.index(td) if td in trading_days else -1
            if td_idx < 252:
                continue

            # Check if any insider purchase in last 252 trading days (by filed_ts date)
            # Need to map filed_ts dates to trading days - use calendar day comparison
            cutoff_252 = trading_days[td_idx - 252]
            has_purchase_252 = any(pd >= cutoff_252 for pd in purchase_dates)
            if not has_purchase_252:
                continue

            # Entry condition 1: at least one insider purchase disclosed in last 5 trading days
            cutoff_5 = trading_days[td_idx - 5]
            has_purchase_5 = any(pd >= cutoff_5 for pd in purchase_dates)
            if not has_purchase_5:
                opportunities.append((sym, td, False))
                continue

            # Entry condition 2: macro 20-day MA declined >=5% from 40 trading days ago
            if not macro_decline_td.get(td, False):
                opportunities.append((sym, td, False))
                continue

            # Entry condition 3: news sentiment 20-day MA < 200-day MA
            # Need sentiment MAs as of td (using latest available on or before td)
            ma20_val = None
            ma200_val = None
            for sd in reversed(sent_dates):
                if sd <= td:
                    ma20_val = sent_ma20[sym].get(sd)
                    ma200_val = sent_ma200[sym].get(sd)
                    break
            if ma20_val is None or ma200_val is None:
                opportunities.append((sym, td, False))
                continue
            if ma20_val >= ma200_val:
                opportunities.append((sym, td, False))
                continue

            # All conditions met - issue call
            opportunities.append((sym, td, True))
            # Get label for this symbol at this decision point for horizon 21
            # The label ts in prediction_outcomes is the decision timestamp
            label_key = (sym, td)
            if label_key in labels:
                issued_calls.append((sym, td, labels[label_key]))

    if not opportunities:
        print("INSUFFICIENT=1")
        return 0

    # 8. Hold out most recent 20% as sealed era
    # Split by decision date (trading day)
    decision_dates = sorted(set(td for _, td, _ in opportunities))
    split_idx = int(len(decision_dates) * 0.8)
    if split_idx == 0:
        print("INSUFFICIENT=1")
        return 0
    train_dates = set(decision_dates[:split_idx])
    test_dates = set(decision_dates[split_idx:])

    # Separate issued calls
    train_issued = [(sym, td, up) for sym, td, up in issued_calls if td in train_dates]
    test_issued = [(sym, td, up) for sym, td, up in issued_calls if td in test_dates]

    # 9. Compute metrics
    def compute_metrics(issued_list, all_opportunities_list):
        if not issued_list:
            return {
                'issued': 0,
                'opportunities': len(all_opportunities_list),
                'precision': 0.0,
                'base_rate': 0.0,
                'distinct_days': 0,
                'effective_n': 0.0
            }
        issued_count = len(issued_list)
        hits = sum(1 for _, _, up in issued_list if up == 1)
        precision = hits / issued_count if issued_count > 0 else 0.0
        base_rate = hits / issued_count if issued_count > 0 else 0.0  # base rate within issued subset
        distinct_days = len(set(td for _, td, _ in issued_list))

        # Design effect: cluster by day, compute variance inflation
        # Simple approach: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use Kish's effective sample size: n_eff = (sum w)^2 / sum(w^2) where w=1 per call
        # But clustered: group by day, each day is a cluster
        day_counts = defaultdict(int)
        for _, td, _ in issued_list:
            day_counts[td] += 1
        cluster_sizes = list(day_counts.values())
        if len(cluster_sizes) > 1:
            # Design effect approx = 1 + (mean_cluster_size - 1) * ICC
            # Conservative: assume ICC=0.1, or use Kish formula for unequal clusters
            # Kish: n_eff = n / (1 + CV^2) where CV is coefficient of variation of cluster sizes
            mean_c = sum(cluster_sizes) / len(cluster_sizes)
            var_c = sum((c - mean_c)**2 for c in cluster_sizes) / len(cluster_sizes)
            cv = (var_c ** 0.5) / mean_c if mean_c > 0 else 0
            design_effect = 1 + cv**2
        else:
            design_effect = 1.0
        effective_n = issued_count / design_effect if design_effect > 0 else issued_count

        return {
            'issued': issued_count,
            'opportunities': len(all_opportunities_list),
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_opps = [o for o in opportunities if o[1] in train_dates]
    test_opps = [o for o in opportunities if o[1] in test_dates]

    train_metrics = compute_metrics(train_issued, train_opps)
    test_metrics = compute_metrics(test_issued, test_opps)

    # 10. Print results
    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={train_metrics['opportunities']}")
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={test_metrics['precision']:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())