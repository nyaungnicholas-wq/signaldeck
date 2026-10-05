import sqlite3
import sys
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Check for any data that might represent S&P 500 index additions
    # The regime_outcomes table might have kind values for index additions
    cursor.execute("SELECT COUNT(*) FROM regime_outcomes WHERE kind LIKE '%index%' OR kind LIKE '%sp500%' OR kind LIKE '%s&p%'")
    if cursor.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)

    # Check if we have enough price data
    cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM bars WHERE tf='1d'")
    if cursor.fetchone()[0] < 50:  # Need at least some symbols
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)

    # We cannot compute market cap from available data
    # We cannot check corporate actions, earnings, or other abstention criteria
    # The data is insufficient for the full hypothesis test
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)

if __name__ == "__main__":
    main()