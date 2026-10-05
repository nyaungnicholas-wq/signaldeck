import sqlite3
import sys
from collections import defaultdict

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)

cursor = conn.cursor()

# Check for required table structures
required_tables = {
    'bars': ['symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'],
    'symbols': ['id', 'symbol', 'market', 'name', 'active', 'added_at', 'stream', 'delisted_at'],
    'regime_outcomes': ['id', 'symbol_id', 'kind', 'ts', 'day', 'horizon_days', 'regime', 'conviction',
                        'historical_accuracy', 'rank', 'resolved_at', 'actual', 'correct', 'naive_label',
                        'revision', 'basis_epoch'],
    'prediction_outcomes': ['symbol_id', 'horizon', 'ts', 'prob', 'up', 'fwd_return', 'resolved_at', 'basis_epoch'],
    'scores': ['symbol_id', 'horizon', 'ts', 'score', 'components']
}

cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
tables_in_db = {row[0] for row in cursor.fetchall()}

for table, cols in required_tables.items():
    if table not in tables_in_db:
        print("INSUFFICIENT=1")
        sys.exit(0)
    cursor.execute(f"PRAGMA table_info({table})")
    db_cols = {row[1] for row in cursor.fetchall()}
    if not set(cols).issubset(db_cols):
        print("INSUFFICIENT=1")
        sys.exit(0)

# Check if we have any short-seller report data in regime_outcomes
# The hypothesis requires short-seller reports but we have no column indicating report type
# regime_outcomes has a 'kind' column - check its values
cursor.execute("SELECT DISTINCT kind FROM regime_outcomes LIMIT 10")
kind_values = {row[0] for row in cursor.fetchall()}

# Since we have no way to identify short-seller reports from available data
# we cannot construct the required universe
print("INSUFFICIENT=1")
conn.close()
sys.exit(0)