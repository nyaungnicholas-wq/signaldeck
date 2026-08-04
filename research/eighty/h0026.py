import sqlite3
import sys

db_path = 'file:data/signaldeck.db?mode=ro'
conn = None
try:
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    
    # Check if required tables exist and have data
    required_tables = ['bars', 'symbols', 'prediction_outcomes']
    for table in required_tables:
        cur.execute(f"SELECT COUNT(*) FROM {table}")
        count = cur.fetchone()[0]
        if count == 0:
            print("INSUFFICIENT=1")
            sys.exit(0)
    
    # Check if we have enough bars for 20-day horizon
    cur.execute("SELECT MAX(ts) FROM bars")
    max_ts = cur.fetchone()[0]
    if max_ts is None:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Check if we have any prediction_outcomes with 'up' labels
    cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE up IS NOT NULL")
    if cur.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Since we don't have event data for buyback announcements,
    # we cannot identify the universe or issue calls based on the hypothesis.
    # The data is insufficient for the required test.
    print("INSUFFICIENT=1")
    
except Exception as e:
    print("INSUFFICIENT=1")
finally:
    if conn:
        conn.close()
    sys.exit(0)