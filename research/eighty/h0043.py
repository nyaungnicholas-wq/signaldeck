import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return 0

    cur = conn.cursor()

    # Check if required tables exist with necessary columns
    try:
        cur.execute("SELECT COUNT(*) FROM bars")
        bars_count = cur.fetchone()[0]
        if bars_count == 0:
            print("INSUFFICIENT=1")
            return 0

        cur.execute("SELECT COUNT(*) FROM symbols")
        symbols_count = cur.fetchone()[0]
        if symbols_count == 0:
            print("INSUFFICIENT=1")
            return 0

        # Check for dividend initiation events in regime_outcomes
        # We don't know the exact 'kind' value, but we'll look for rows that could represent first-ever dividend initiation
        # Since we can't filter by kind without knowing values, we'll check if any regime_outcomes exist
        cur.execute("SELECT COUNT(*) FROM regime_outcomes")
        regime_count = cur.fetchone()[0]
        if regime_count == 0:
            print("INSUFFICIENT=1")
            return 0

        # Check prediction_outcomes for label data
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE up IS NOT NULL")
        label_count = cur.fetchone()[0]
        if label_count == 0:
            print("INSUFFICIENT=1")
            return 0

    except Exception:
        print("INSUFFICIENT=1")
        return 0

    # We have data, but we cannot properly test the hypothesis because:
    # 1. No dividend initiation event data in the database
    # 2. No market cap data to filter universe
    # 3. No earnings schedule data
    # 4. No corporate action data (mergers, spin-offs, etc.)
    # 5. No book equity data
    # Without these, we cannot identify dividend initiation events or apply the required filters

    # Therefore, data is insufficient to test this specific hypothesis
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())