# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 396
# cycle_index: 64
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get active US stock symbols
    cur.execute("SELECT id FROM symbols WHERE market='stocks' AND active=1")
    symbol_ids = {row['id'] for row in cur.fetchall()}
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return

    # Find available horizons in prediction_outcomes with sufficient data
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT horizon, COUNT(*) as cnt
        FROM prediction_outcomes
        WHERE symbol_id IN ({placeholders}) AND up IS NOT NULL
        GROUP BY horizon
        ORDER BY cnt DESC
    """, list(symbol_ids))
    horizons = cur.fetchall()
    if not horizons:
        print("INSUFFICIENT=1")
        return

    # Use horizon with most data (prefer 5 if available)
    target_horizon = 5
    horizon_counts = {h['horizon']: h['cnt'] for h in horizons}
    if target_horizon not in horizon_counts:
        target_horizon = max(horizon_counts, key=horizon_counts.get)

    # Get sentiment features for our symbols with entry criteria
    # mean_score < -0.15 and n_all >= 10
    cur.execute(f"""
        SELECT symbol_id, day, mean_score, n_all
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders}) AND mean_score < -0.15 AND n_all >= 10
        ORDER BY day
    """, list(symbol_ids))
    sentiment_rows = cur.fetchall()
    if not sentiment_rows:
        print("INSUFFICIENT=1")
        return

    # Get prediction outcomes for target horizon
    cur.execute(f"""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE symbol_id IN ({placeholders}) AND horizon = ? AND up IS NOT NULL
    """, list(symbol_ids) + [target_horizon])
    outcome_rows = cur.fetchall()
    if not outcome_rows:
        print("INSUFFICIENT=1")
        return

    # Build outcome lookup: (symbol_id, date(ts)) -> (up, fwd_return)
    # ts is unix epoch; we want the date of the prediction start
    outcome_map = {}
    for row in outcome_rows:
        dt = datetime.utcfromtimestamp(row['ts']).date()
        outcome_map[(row['symbol_id'], dt)] = (row['up'], row['fwd_return'])

    # Match sentiment day D to outcome date D+1 (next calendar day)
    # Sentiment from day D available at start of D+1
    observations = []  # (symbol_id, day, predicted_neg, actual_neg)
    for row in sentiment_rows:
        sym = row['symbol_id']
        day = datetime.strptime(row['day'], '%Y-%m-%d').date()
        next_day = day + timedelta(days=1)
        key = (sym, next_day)
        if key in outcome_map:
            up, fwd = outcome_map[key]
            actual_neg = 1 if (up == 0 or fwd < 0) else 0
            observations.append((sym, day, 1, actual_neg))  # predicted_neg=1 (we predict negative)

    if not observations:
        print("INSUFFICIENT=1")
        return

    # Sort by day for temporal holdout
    observations.sort(key=lambda x: x[1])
    n_total = len(observations)
    n_sealed = max(1, int(n_total * 0.2))
    train_obs = observations[:-n_sealed]
    sealed_obs = observations[-n_sealed:]

    def compute_metrics(obs_list):
        if not obs_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(obs_list)
        hits = sum(1 for o in obs_list if o[3] == 1)  # actual_neg == 1
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate of predicted class (negative) within issued
        distinct_days = len(set(o[1] for o in obs_list))
        # Design effect: conservative estimate based on clustering
        avg_per_day = issued / distinct_days if distinct_days else 1
        design_effect = max(1.01, 1 + 0.5 * (avg_per_day - 1))
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(train_obs)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_obs)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={n_total}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()