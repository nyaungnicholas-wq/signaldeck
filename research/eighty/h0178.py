import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return

    try:
        cursor = conn.cursor()
        # Check if we have any data that could possibly represent S&P 500 additions.
        # Without explicit event data, we cannot proceed.
        # We check for the presence of required tables and basic feasibility.
        tables_needed = {'bars', 'symbols', 'prediction_outcomes'}
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables_found = {row[0] for row in cursor.fetchall()}
        if not tables_needed.issubset(tables_found):
            print("INSUFFICIENT=1")
            return

        # Check for any price data in bars
        cursor.execute("SELECT COUNT(*) FROM bars")
        bar_count = cursor.fetchone()[0]
        if bar_count == 0:
            print("INSUFFICIENT=1")
            return

        # Check for any prediction outcomes
        cursor.execute("SELECT COUNT(*) FROM prediction_outcomes")
        pred_count = cursor.fetchone()[0]
        if pred_count == 0:
            print("INSUFFICIENT=1")
            return

        # The core issue: no table provides S&P 500 addition events.
        # We cannot identify the required universe (S&P 500 additions with specific criteria).
        # Therefore data is insufficient.
        print("INSUFFICIENT=1")
    except Exception:
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()