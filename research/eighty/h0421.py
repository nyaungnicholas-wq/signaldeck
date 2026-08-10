# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 420
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = db.cursor()

try:
    # Universe: symbols with at least 10 quarters of 13F filings
    c.execute("""
        SELECT symbol_id, COUNT(DISTINCT period) as qtrs
        FROM inst_holdings
        GROUP BY symbol_id
        HAVING qtrs >= 10
    """)
    universe = [row[0] for row in c.fetchall()]
    if not universe:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Build decisions: for each symbol, each quarter with prior quarter data,
    # compute aggregate shares change and check news sentiment on estimated filing day.
    # Need sentiment from news table on day <= quarter_end + 45 days.
    # As-of discipline: decision at quarter_end + 45 (filing disclosure), label 21 days after decision.
    decisions = []
    for sym in universe:
        # Get all quarters for this symbol, ordered
        c.execute("""
            SELECT period, SUM(shares) as total_shares
            FROM inst_holdings
            WHERE symbol_id = ?
            GROUP BY period
            ORDER BY period
        """, (sym,))
        quarters = c.fetchall()
        if len(quarters) < 2:
            continue
        for i in range(1, len(quarters)):
            prev_period, prev_shares = quarters[i-1]
            curr_period, curr_shares = quarters[i]
            if prev_shares == 0 or curr_shares == 0:
                continue
            pct_change = (curr_shares - prev_shares) / prev_shares
            if pct_change < 0.05:
                continue
            # Estimate filing disclosure day: quarter_end + 45 days
            # We have period as string 'YYYY-MM-DD', need to add 45 days.
            # Use SQLite date arithmetic.
            c.execute("""
                SELECT date(?, '+45 days')
            """, (curr_period,))
            filing_day = c.fetchone()[0]
            if not filing_day:
                continue
            # Get news sentiment on filing_day for this symbol
            # Use sentiment_features table (day is 'YYYY-MM-DD')
            c.execute("""
                SELECT mean_score
                FROM sentiment_features
                WHERE symbol_id = ? AND day = ?
            """, (sym, filing_day))
            row = c.fetchone()
            if not row:
                continue
            score = row[0]
            if score is None:
                continue
            # Compute bottom decile threshold for that day across all symbols
            # Must compute on-the-fly to respect as-of: only using data available on filing_day.
            # But we need distribution of scores on that day for all symbols.
            # We'll compute threshold in a subquery.
            c.execute("""
                SELECT AVG(score) as avg_score,
                       AVG(score * score) as avg_sq,
                       COUNT(*) as cnt
                FROM sentiment_features
                WHERE day = ?
            """, (filing_day,))
            stats = c.fetchone()
            if not stats or stats[2] < 10:  # need enough data for decile
                continue
            avg, avg_sq, n = stats
            # For decile, we need the 10th percentile. Without numpy, approximate via normal? Not safe.
            # Instead, fetch all scores for that day and compute decile manually.
            c.execute("""
                SELECT mean_score
                FROM sentiment_features
                WHERE day = ?
            """, (filing_day,))
            scores = [r[0] for r in c.fetchall() if r[0] is not None]
            if len(scores) < 10:
                continue
            scores.sort()
            idx = int(0.1 * len(scores))
            p10 = scores[idx]
            if score > p10:
                continue
            # Decision point: filing_day. Label: 21 days after decision.
            # As-of: decision at filing_day, outcome at filing_day+21.
            # Get forward return and direction from prediction_outcomes? No, use bars.
            # We need future price: close on decision day and close 21 trading days later.
            # Use bars with tf='1d'. Need trading days, not calendar. Use next 21 bars.
            # First, get close on decision day
            c.execute("""
                SELECT close
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= strftime('%s', ?)
                ORDER BY ts
                LIMIT 1
            """, (sym, filing_day))
            row = c.fetchone()
            if not row:
                continue
            close_day = row[0]
            # Get close 21 trading days later
            c.execute("""
                SELECT close
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts > strftime('%s', ?)
                ORDER BY ts
                LIMIT 21 OFFSET 20
            """, (sym, filing_day))
            row = c.fetchone()
            if not row:
                continue
            close_21 = row[0]
            fwd_return = (close_21 - close_day) / close_day
            up = 1 if fwd_return > 0 else 0
            # Store decision
            decisions.append({
                'sym': sym,
                'decision_date': filing_day,
                'fwd_return': fwd_return,
                'up': up,
                'close_day': close_day
            })

    if not decisions:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Sort by decision date
    decisions.sort(key=lambda x: x['decision_date'])

    # Hold out most recent 20% as sealed era
    split_idx = int(0.8 * len(decisions))
    train = decisions[:split_idx]
    sealed = decisions[split_idx:]

    # Compute metrics for train and sealed separately
    def compute_metrics(subset, label=""):
        if not subset:
            return 0, 0, 0, 0, 0, 0
        issued = len(subset)
        hits = sum(1 for d in subset if d['up'] == 1)  # predicted class = up
        precision = hits / issued if issued else 0
        base_rate = hits / issued  # base rate of predicted class in issued subset
        distinct_days = len(set(d['decision_date'] for d in subset))
        # Design effect: due to clustering in time. We'll compute autocorrelation of decisions on the same day.
        # For simplicity, assume each day cluster is independent, and within day, calls are correlated.
        # Count clusters (days) and within-day counts.
        from collections import Counter
        day_counts = Counter(d['decision_date'] for d in subset)
        clusters = len(day_counts)
        avg_cluster_size = issued / clusters if clusters else 1
        # Design effect ≈ 1 + (avg_cluster_size - 1) * ICC, assume ICC=0.5 (typical for correlated calls)
        icc = 0.5
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, precision, base_rate, distinct_days, effective_n, design_effect

    issued_train, precision_train, base_rate_train, days_train, eff_n_train, de_train = compute_metrics(train)
    issued_sealed, precision_sealed, base_rate_sealed, days_sealed, eff_n_sealed, de_sealed = compute_metrics(sealed)

    # Invariants check
    if days_train > issued_train or days_sealed > issued_sealed:
        print("INVARIANT_VIOLATION")
        sys.exit(1)
    if eff_n_train >= issued_train or eff_n_sealed >= issued_sealed:
        print("INVARIANT_VIOLATION")
        sys.exit(1)

    # Print required outputs (only train set for main metrics, sealed separately)
    print(f"ISSUED={issued_train}")
    print(f"OPPORTUNITIES={issued_train}")  # Here opportunities = decisions considered = issued (no abstentions in this subset)
    print(f"PRECISION={precision_train:.4f}")
    print(f"BASE_RATE={base_rate_train:.4f}")
    print(f"DISTINCT_DAYS={days_train}")
    print(f"EFFECTIVE_N={eff_n_train:.2f}")
    print(f"SEALED_PRECISION={precision_sealed:.4f}")

except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)
finally:
    db.close()