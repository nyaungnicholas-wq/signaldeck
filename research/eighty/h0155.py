import sqlite3

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
c = conn.cursor()

# Check for upgrade events
c.execute("SELECT COUNT(*) FROM regime_outcomes WHERE kind LIKE '%upgrade%'")
if c.fetchone()[0] == 0:
    print("INSUFFICIENT=1")
    exit(0)

# Check for market cap data
c.execute("PRAGMA table_info(symbols)")
symbol_cols = [row[1] for row in c.fetchall()]
if 'market_cap' not in symbol_cols:
    print("INSUFFICIENT=1")
    exit(0)

conn.close()
print("INSUFFICIENT=1")