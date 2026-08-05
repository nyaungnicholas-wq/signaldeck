import sqlite3
import sys

def main():
    conn = None
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        tables = {row[0] for row in conn.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        required_tables = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
        if not required_tables <= tables:
            print("INSUFFICIENT=1")
            return 0
        bar_cols = {row[1] for row in conn.execute("PRAGMA table_info(bars)")}
        if not {'symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'} <= bar_cols:
            print("INSUFFICIENT=1")
            return 0
    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return 0
    finally:
        if conn is not None:
            conn.close()

    # The documented schema has no offering/announcement table and no columns
    # for offering size, shares outstanding, offering type, concurrent events,
    # earnings calendar, book equity, going concern, or listing age. The
    # hypothesis therefore cannot be measured from this database.
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())