import sqlite3
import sys

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

# Check for required tables
required_tables = {'bars', 'symbols', 'prediction_outcomes'}
cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
existing = {row[0] for row in cur.fetchall()}
if not required_tables.issubset(existing):
    print("INSUFFICIENT=1")
    sys.exit(0)

# Need earnings announcement data to compute SUE and identify events.
# The provided tables have no earnings data (no earnings dates, EPS, SUE scores).
# Without this, we cannot identify the universe or compute the hypothesis.
print("INSUFFICIENT=1")
sys.exit(0)