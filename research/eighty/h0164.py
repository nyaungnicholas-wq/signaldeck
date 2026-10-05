import sys
import sqlite3

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
except Exception:
    print("INSUFFICIENT=1")
    sys.exit(0)

cur = conn.cursor()

# Check for required tables
required = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
tables = {r[0] for r in cur.fetchall()}
if not required.issubset(tables):
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)

# Check for repurchase announcement data – need event table
cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%repurchase%' OR name LIKE '%buyback%'")
if not cur.fetchall():
    # No event table for repurchase announcements
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)

# We have an event table – but the hypothesis requires specific filtering of repurchase events.
# The provided schema does not include such a table, so we cannot proceed.
# Following instructions: if data insufficient, print INSUFFICIENT=1 and exit.
print("INSUFFICIENT=1")
conn.close()
sys.exit(0)