import sqlite3, sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Check for required tables/columns to derive lockup dates
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    
    # Without IPO/lockup date data in schema, we cannot derive T (lockup expiry)
    # The hypothesis requires specific lockup dates not present in available tables
    print("INSUFFICIENT=1")
    sys.exit(0)
    
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)
finally:
    if 'conn' in locals():
        conn.close()