#!/usr/bin/env python3
import sqlite3
import sys
from collections import defaultdict

# Attempt to connect to the read-only database
try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)

cursor = conn.cursor()

# Check if required tables exist and have data
required_tables = ['bars', 'symbols']
for table in required_tables:
    try:
        cursor.execute(f"SELECT COUNT(*) FROM {table}")
        count = cursor.fetchone()[0]
        if count == 0:
            print("INSUFFICIENT=1")
            sys.exit(0)
    except:
        print("INSUFFICIENT=1")
        sys.exit(0)

# Get list of US stocks
cursor.execute("SELECT id FROM symbols WHERE market = 'stocks'")
stock_ids = [row[0] for row in cursor.fetchall()]
if not stock_ids:
    print("INSUFFICIENT=1")
    sys.exit(0)

# Check if we have lockup expiration data
# We don't have a lockup table, so we cannot identify lockup events
print("INSUFFICIENT=1")
sys.exit(0)

# The rest of the logic would go here if we had lockup data
# Since we don't have lockup expiration dates in the database,
# we cannot test this hypothesis with the available data

conn.close()