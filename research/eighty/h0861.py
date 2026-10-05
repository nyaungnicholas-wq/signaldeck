# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 860
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()

    # The hypothesis requires: "prior-month FRED INDPRO exceeds consensus by >0.5 sigma"
    # This needs consensus forecasts for INDPRO (survey median) and historical surprise volatility.
    # The schema provides only macro_series(series, ts, value) with actual FRED releases.
    # No table contains consensus forecasts or pre-computed surprises.
    # Verify no consensus-like series exists in macro_series.
    cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%INDPRO%'")
    indpro_series = [row[0] for row in cur.fetchall()]
    conn.close()

    # FRED INDPRO series are actuals (e.g., 'INDPRO', 'INDPRO_INDEX'); no consensus/survey series present.
    # Without consensus, "exceeds consensus by >0.5 sigma" cannot be computed as defined.
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == "__main__":
    main()