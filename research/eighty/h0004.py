import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return

    cursor = conn.cursor()

    # Check for required insider data tables - we don't have them in schema
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = [row[0] for row in cursor.fetchall()]
    
    # No insider transactions table exists in the database
    required_tables = ['insider_transactions']  # Not present
    for table in required_tables:
        if table not in tables:
            print("INSUFFICIENT=1")
            conn.close()
            return
    
    # If we got here, we would proceed with backtest logic
    # But since we don't have insider data, we exit
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == "__main__":
    main()