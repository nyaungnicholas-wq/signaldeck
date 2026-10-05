import sqlite3
import sys

con = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
try:
    con.execute("SELECT 1 FROM symbols LIMIT 1").fetchone()
finally:
    con.close()

# No spin-off announcement source or required corporate-action/fundamental
# fields exist in the listed schema, so the hypothesis cannot be tested
# without fabricating inputs.
print("INSUFFICIENT=1")
sys.exit(0)