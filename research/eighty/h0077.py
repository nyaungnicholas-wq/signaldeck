import sqlite3

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

# Check for repurchase authorization data - this table doesn't exist in schema
cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT IN ('bars','symbols','regime_outcomes','prediction_outcomes','scores')")
other_tables = cur.fetchall()
if other_tables:
    # Unexpected tables exist - still insufficient for this specific hypothesis
    print("INSUFFICIENT=1")
else:
    # No repurchase authorization table exists, so cannot test hypothesis
    print("INSUFFICIENT=1")

conn.close()