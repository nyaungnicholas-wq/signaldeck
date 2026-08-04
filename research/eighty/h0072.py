import sqlite3
import sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)

# Check for 13D filing data (required for the hypothesis)
cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
tables = {row[0] for row in cur.fetchall()}

# Required filing data tables do not exist in schema
if not tables.intersection(['filings', 'sec_filings', 'schedule_13d', 'activist_events']):
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)

# Check for required columns in any filing table
filing_tables = tables.intersection(['filings', 'sec_filings', 'schedule_13d', 'activist_events'])
has_required_columns = False

for table in filing_tables:
    cur.execute(f"PRAGMA table_info({table})")
    columns = {row[1] for row in cur.fetchall()}
    required = {'symbol_id', 'filing_date', 'filing_type', 'filer_type', 'stake_percent'}
    if required.issubset(columns):
        has_required_columns = True
        break

if not has_required_columns:
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)

conn.close()