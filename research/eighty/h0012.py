import sqlite3
import sys

def main():
    con = sqlite3.connect("file:data/signaldeck.db?mode=ro", uri=True)
    try:
        tables = {r[0].lower() for r in con.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        )}
    finally:
        con.close()

    # The supplied schema has only bars, symbols, regime_outcomes,
    # prediction_outcomes, and scores. It contains no Form 4 filings,
    # earnings calendar, buyback announcements, or market-cap data.
    has_form4 = any(any(k in t for k in ("form4", "form_4", "insider", "filing")) for t in tables)
    has_earnings = any("earning" in t for t in tables)
    has_buybacks = any(any(k in t for k in ("buyback", "repurchase")) for t in tables)

    if not (has_form4 and has_earnings and has_buybacks):
        print("INSUFFICIENT=1")
        return 0

    return 0

if __name__ == "__main__":
    sys.exit(main())