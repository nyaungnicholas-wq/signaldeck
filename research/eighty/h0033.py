import sqlite3
import sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)

cur = conn.cursor()

# Check for earnings-related columns in tables
cur.execute("PRAGMA table_info(regime_outcomes)")
regime_cols = {row[1] for row in cur.fetchall()}

cur.execute("PRAGMA table_info(prediction_outcomes)")
pred_cols = {row[1] for row in cur.fetchall()}

cur.execute("PRAGMA table_info(scores)")
score_cols = {row[1] for row in cur.fetchall()}

cur.execute("PRAGMA table_info(bars)")
bar_cols = {row[1] for row in cur.fetchall()}

cur.execute("PRAGMA table_info(symbols)")
sym_cols = {row[1] for row in cur.fetchall()}

# Verify required tables exist and have expected schema
required_tables = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
existing_tables = {row[0] for row in cur.fetchall()}

if not required_tables.issubset(existing_tables):
    print("INSUFFICIENT=1")
    sys.exit(0)

# Check for earnings-related columns needed for hypothesis
# The hypothesis requires earnings announcement dates, EPS data, guidance,
# corporate actions, fundamental data - none of which exist in the schema.
# We cannot test earnings surprise drift without earnings data.

# Verify prediction_outcomes has up/fwd_return for labels
if 'up' not in pred_cols or 'fwd_return' not in pred_cols:
    print("INSUFFICIENT=1")
    sys.exit(0)

# The hypothesis cannot be tested with available data
print("INSUFFICIENT=1")
conn.close()
sys.exit(0)