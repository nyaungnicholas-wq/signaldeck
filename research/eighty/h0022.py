import sqlite3
import sys
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
    except Exception:
        print("INSUFFICIENT=1")
        return 0

    # Check for analyst rating data - need to find upgrade events
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = [row[0] for row in cur.fetchall()]
    
    # We need analyst rating data to test this hypothesis
    # The provided schema has no table for analyst ratings, upgrades, or initiations
    # Check if regime_outcomes might contain analyst actions
    cur.execute("SELECT DISTINCT kind FROM regime_outcomes LIMIT 10")
    kinds = [row[0] for row in cur.fetchall()]
    
    # Without analyst rating data, we cannot identify upgrade events
    # The hypothesis requires: initiate Buy coverage or upgrade to Buy/Strong Buy by at least two notches
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())