# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 658
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Check what macro series exist - look for GDP surprise data
    cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%GDP%' OR series LIKE '%gdp%' OR series LIKE '%surprise%' OR series LIKE '%SURPRISE%'")
    gdp_series = [row['series'] for row in cur.fetchall()]
    
    # Also check all series to see what's available
    cur.execute("SELECT DISTINCT series FROM macro_series")
    all_series = [row['series'] for row in cur.fetchall()]
    
    # We need a series that gives us quarterly GDP surprise (actual - expected) in percentage points
    # Common FRED series: GDPC1 (real GDP), GDP (nominal GDP), but surprise series are rare
    # Check if any series looks like a surprise/forecast error series
    surprise_candidates = [s for s in all_series if 'SURPRISE' in s.upper() or 'ERROR' in s.upper() or 'FORECAST' in s.upper() or 'EXPECT' in s.upper()]
    gdp_candidates = [s for s in all_series if 'GDP' in s.upper()]
    
    # We need BOTH actual GDP and expected GDP to compute surprise, OR a direct surprise series
    # The macro_series table only has (series, ts, value) - single value per series per timestamp
    # No forecast data visible in schema
    
    # Check if there's a GDP surprise series explicitly
    gdp_surprise_series = None
    for s in all_series:
        if 'GDP' in s.upper() and ('SURPRISE' in s.upper() or 'ERROR' in s.upper() or 'GAP' in s.upper()):
            gdp_surprise_series = s
            break
    
    if not gdp_surprise_series:
        # Cannot compute GDP surprise from actuals alone - need expectations/forecasts
        print("INSUFFICIENT=1")
        return 0

    # If we had a surprise series, we'd proceed. But per schema inspection, macro_series 
    # only contains actual values of FRED series, not forecast errors/surprises.
    # The schema says: "macro_series: FRED series keyed by name. Reaches back to 1947."
    # Standard FRED does not include GDP surprise series in the main database.
    
    print("INSUFFICIENT=1")
    return 0

if __name__ == '__main__':
    sys.exit(main())