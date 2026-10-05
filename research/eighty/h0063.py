import sqlite3
import sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Check if required tables exist
    c.execute("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('bars', 'symbols')")
    tables = {row[0] for row in c.fetchall()}
    
    if not tables.issuperset({'bars', 'symbols'}):
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
    
    # Verify we can read from both tables
    c.execute("SELECT COUNT(*) FROM bars")
    bars_count = c.fetchone()[0]
    c.execute("SELECT COUNT(*) FROM symbols")
    symbols_count = c.fetchone()[0]
    
    if bars_count == 0 or symbols_count == 0:
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
    
    # We need repurchase authorization data which is not in the schema
    # The hypothesis requires corporate action data that doesn't exist in the provided tables
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)
    
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)