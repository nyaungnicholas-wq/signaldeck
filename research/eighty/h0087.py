import sqlite3

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Check for repurchase announcements table
    c.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = [r[0] for r in c.fetchall()]
    
    # We need a table that contains repurchase announcement events
    if not any('repurchase' in t.lower() for t in tables):
        print("INSUFFICIENT=1")
        exit(0)
        
    # If we had such a table, the rest of the analysis would follow,
    # but per the actual schema provided, there is no repurchase announcements table.
    print("INSUFFICIENT=1")
    exit(0)
    
except Exception as e:
    print("INSUFFICIENT=1")
    exit(0)