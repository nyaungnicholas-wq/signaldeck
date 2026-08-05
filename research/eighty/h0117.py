import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)
    cur = conn.cursor()
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = [row[0] for row in cur.fetchall()]
    required = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
    if not required.issubset(set(tables)):
        print("INSUFFICIENT=1")
        sys.exit(0)
    # Check for earnings data
    cur.execute("PRAGMA table_info(symbols)")
    sym_cols = {r[1] for r in cur.fetchall()}
    if 'earnings_date' not in sym_cols and 'sue' not in sym_cols:
        cur.execute("PRAGMA table_info(bars)")
        bar_cols = {r[1] for r in cur.fetchall()}
        if 'earnings_date' not in bar_cols and 'sue' not in bar_cols:
            # Check for any table with earnings-related columns
            for t in tables:
                cur.execute(f"PRAGMA table_info({t})")
                cols = {r[1] for r in cur.fetchall()}
                if any(kw in col.lower() for col in cols for kw in ['earn', 'sue', 'surprise']):
                    break
            else:
                print("INSUFFICIENT=1")
                sys.exit(0)
    # We have earnings data but need to verify market cap etc.
    # Without the required columns, we cannot proceed
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == '__main__':
    main()