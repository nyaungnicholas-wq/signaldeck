import sqlite3
from datetime import datetime, timezone
from collections import defaultdict
import math

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    try:
        conn = sqlite3.connect(db_path, uri=True, timeout=30)
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all symbols with public float (fundamentals)
    cur.execute("""
        SELECT symbol_id, MAX(fetched_at) as fetched
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat'