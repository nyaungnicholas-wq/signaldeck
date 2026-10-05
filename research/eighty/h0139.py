import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Check for necessary tables and columns
    cursor = conn.cursor()
    try:
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cursor.fetchall()}
        if not {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}.issubset(tables):
            print("INSUFFICIENT=1")
            sys.exit(0)
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # We cannot identify Russell 3000 deletion events from the given schema.
    # The regime_outcomes table does not clearly map to index reconstitution events.
    # Without explicit deletion dates, we cannot proceed.
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == "__main__":
    main()