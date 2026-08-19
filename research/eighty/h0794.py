# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 793
# cycle_index: 63
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    try:
        cur = conn.cursor()
        # Check stocktwits_sentiment date range
        cur.execute("SELECT MIN(ts), MAX(ts), COUNT(DISTINCT symbol_id) FROM stocktwits_sentiment")
        row = cur.fetchone()
        if not row or row[0] is None:
            print("INSUFFICIENT=1")
            return 0
        min_ts, max_ts, n_symbols = row
        # ts is unix epoch; check span in days
        span_days = (max_ts - min_ts) / 86400
        # Hypothesis requires "multi-year high" for bearish count - need years of history
        if span_days < 365 * 2:  # less than 2 years
            print("INSUFFICIENT=1")
            return 0
        # Also need prediction_outcomes for labels with sufficient history
        cur.execute("SELECT MIN(ts), MAX(ts) FROM prediction_outcomes WHERE horizon IN ('1d','1w')")
        row2 = cur.fetchone()
        if not row2 or row2[0] is None:
            print("INSUFFICIENT=1")
            return 0
        # Check insider_trades has enough history
        cur.execute("SELECT MIN(tx_ts), MAX(tx_ts) FROM insider_trades WHERE code = 'P'")
        row3 = cur.fetchone()
        if not row3 or row3[0] is None:
            print("INSUFFICIENT=1")
            return 0
        # If we reach here, data might be sufficient - but hypothesis is incomplete in prompt
        # The prompt cuts off at "retail capitulation" - cannot implement incomplete hypothesis
        print("INSUFFICIENT=1")
        return 0
    finally:
        conn.close()

if __name__ == "__main__":
    sys.exit(main())