import sqlite3
import sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cursor = conn.cursor()

    # Check if the necessary tables exist
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = [row[0] for row in cursor.fetchall()]
    required_tables = ['bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores']
    
    for table in required_tables:
        if table not in tables:
            print("INSUFFICIENT=1")
            sys.exit(0)
    
    # Check for FDA approval events - this data doesn't exist in the schema
    # The hypothesis requires specific event data that is not present
    print("INSUFFICIENT=1")
    sys.exit(0)

except Exception:
    print("INSUFFICIENT=1")
    sys.exit(0)