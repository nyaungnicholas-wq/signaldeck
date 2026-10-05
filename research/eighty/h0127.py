import sqlite3
import sys

def main():
    try:
        con = sqlite3.connect("file:data/signaldeck.db?mode=ro", uri=True)
    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return 0

    try:
        tables = {r[0] for r in con.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        required = {"bars", "symbols", "regime_outcomes", "prediction_outcomes", "scores"}
        if not required.issubset(tables):
            print("INSUFFICIENT=1")
            return 0

        # The hypothesis requires fallen-angel rating downgrade events.  The
        # provided schema contains no rating, downgrade, or announcement data,
        # so those events cannot be identified without fabrication.
        print("INSUFFICIENT=1")
        return 0
    finally:
        con.close()

if __name__ == "__main__":
    sys.exit(main())