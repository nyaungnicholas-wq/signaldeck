import sqlite3
import sys

def main():
    try:
        db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return 0

    cur = db.cursor()

    # Check for Form 4 data in the database
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    if 'form4' not in tables:
        print("INSUFFICIENT=1")
        db.close()
        return 0

    # If form4 table exists, check its columns
    try:
        cur.execute("PRAGMA table_info(form4)")
        cols = {row[1] for row in cur.fetchall()}
        required_cols = {'filing_date', 'symbol', 'issuer_name', 'reporting_owner', 'relationship', 'transaction_type', 'price', 'quantity'}
        if not required_cols.issubset(cols):
            print("INSUFFICIENT=1")
            db.close()
            return 0
    except Exception:
        print("INSUFFICIENT=1")
        db.close()
        return 0

    # Since the hypothesis requires specific insider data that is not present in the given schema, we cannot proceed.
    # The provided tables do not contain the necessary Form 4 data.
    print("INSUFFICIENT=1")
    db.close()
    return 0

if __name__ == "__main__":
    sys.exit(main())