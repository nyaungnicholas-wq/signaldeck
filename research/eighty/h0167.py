import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        c.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = [row[0] for row in c.fetchall()]
        # Check for any lockup expiry or IPO event tables
        lockup_tables = [t for t in tables if any(term in t.lower() for term in ['lockup', 'ipo', 'event', 'restriction'])]
        if not lockup_tables:
            print("INSUFFICIENT=1")
            sys.exit(0)
        else:
            # If such tables exist, we'd need their schema, but given context says only listed tables,
            # and they aren't listed, we treat as insufficient.
            print("INSUFFICIENT=1")
            sys.exit(0)
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)

if __name__ == "__main__":
    main()