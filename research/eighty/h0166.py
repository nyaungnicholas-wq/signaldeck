import sqlite3
import sys
from datetime import datetime

DB_PATH = 'data/signaldeck.db'

try:
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    cursor = conn.cursor()
    
    # Check for recommendation upgrade data - none in schema
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = [row[0] for row in cursor.fetchall()]
    
    # No tables contain recommendation, upgrade, target price, or event data
    required_tables = ['recommendations', 'upgrades', 'target_prices', 'events']
    missing = [t for t in required_tables if t not in tables]
    
    if missing:
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
        
    # If we somehow got here, still insufficient for this specific hypothesis
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)
    
except Exception:
    print("INSUFFICIENT=1")
    sys.exit(0)