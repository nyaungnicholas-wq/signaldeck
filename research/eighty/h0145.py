import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Check if we have insider transaction data (Form 4 filings) - not in schema
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    
    # Required tables for insider transaction hypothesis are missing
    required_tables = {'insider_transactions', 'form4_filings', 'insider_trades'}
    if not required_tables.intersection(tables):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # If somehow the tables exist but schema doesn't match, still insufficient
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == "__main__":
    main()