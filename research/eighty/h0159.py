#!/usr/bin/env python3
import sqlite3

db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=5)
db.row_factory = sqlite3.Row
cursor = db.cursor()

# Check if we have the necessary tables and columns
required_tables = ['bars', 'symbols']
try:
    for table in required_tables:
        cursor.execute(f"SELECT name FROM sqlite_master WHERE type='table' AND name=?", (table,))
        if not cursor.fetchone():
            print("INSUFFICIENT=1")
            exit(0)
except Exception as e:
    print("INSUFFICIENT=1")
    exit(0)

# Check for the existence of secondary offering data in our schema
# We need: symbol_id, ts (announcement), offering type, size, etc.
# Our tables have no such columns. We cannot identify secondary offerings.
print("INSUFFICIENT=1")
exit(0)