# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 866
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check fundamentals fetched_at range (when data becomes knowable)
    cur.execute("SELECT MIN(fetched_at), MAX(fetched_at) FROM fundamentals")
    f_min, f_max = cur.fetchone()
    print(f"Fundamentals fetched_at range: {f_min} to {f_max}", file=sys.stderr)

    # Check bars max date (for building 63-day forward labels)
    cur.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'")
    bars_max_ts = cur.fetchone()[0]
    print(f"Bars max ts (1d): {bars_max_ts}", file=sys.stderr)

    # Check inst_holdings period range
    cur.execute("SELECT MIN(period), MAX(period) FROM inst_holdings")
    ih_min, ih_max = cur.fetchone()
    print(f"Inst holdings period range: {ih_min} to {ih_max}", file=sys.stderr)

    # Check news/sentiment date ranges
    cur.execute("SELECT MIN(ts), MAX(ts) FROM news")
    n_min, n_max = cur.fetchone()
    print(f"News ts range: {n_min} to {n_max}", file=sys.stderr)

    cur.execute("SELECT MIN(day), MAX(day) FROM sentiment_features")
    sf_min, sf_max = cur.fetchone()
    print(f"Sentiment features day range: {sf_min} to {sf_max}", file=sys.stderr)

    # Check prediction_outcomes horizons and date range
    cur.execute("SELECT DISTINCT horizon FROM prediction_outcomes")
    horizons = [r[0] for r in cur.fetchall()]
    print(f"Prediction outcomes horizons: {horizons}", file=sys.stderr)

    cur.execute("SELECT MIN(ts), MAX(ts) FROM prediction_outcomes")
    po_min, po_max = cur.fetchone()
    print(f"Prediction outcomes ts range: {po_min} to {po_max}", file=sys.stderr)

    # For 63-trading-day horizon from bars, latest decision ts = bars_max_ts - 63*86400 (approx)
    # 63 trading days ≈ 89 calendar days
    latest_decision_ts = bars_max_ts - 89 * 86400
    print(f"Latest decision ts for 63-day horizon: {latest_decision_ts}", file=sys.stderr)

    # Fundamentals only knowable after f_min (fetched_at)
    # Need f_min <= latest_decision_ts for any overlap
    if f_min and latest_decision_ts:
        if f_min > latest_decision_ts:
            print("INSUFFICIENT=1")
            return 0

    # Also need 3+ quarters of fundamentals (as_of) and 13F before decision dates
    # But the fetched_at constraint alone makes it impossible
    print("INSUFFICIENT=1")
    return 0

if __name__ == '__main__':
    sys.exit(main())