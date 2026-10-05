import sqlite3
import sys
import math

DB_PATH = "data/signaldeck.db"

def connect_ro():
    return sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)

def main():
    try:
        conn = connect_ro()
        cur = conn.cursor()
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Check if we have any split data at all - we don't have a splits table
    # so we cannot identify split announcements
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)

if __name__ == "__main__":
    main()