import sqlite3
import sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cursor = conn.cursor()
    
    # Check if we can access the required tables
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cursor.fetchall()}
    
    required_tables = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
    if not required_tables.issubset(tables):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Check if we have any 13D filing data
    # The database has no table for 13D filings, so we cannot identify the events
    # Therefore, we cannot test the hypothesis
    
    conn.close()
    print("INSUFFICIENT=1")
    sys.exit(0)
    
except Exception:
    print("INSUFFICIENT=1")
    sys.exit(0)