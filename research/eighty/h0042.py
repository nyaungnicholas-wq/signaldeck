import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # Check for necessary columns in tables
        required = {
            'bars': ['symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'],
            'symbols': ['id', 'symbol', 'market'],
            'prediction_outcomes': ['symbol_id', 'horizon', 'ts', 'prob', 'up', 'fwd_return']
        }
        
        c.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {r[0] for r in c.fetchall()}
        
        for table, cols in required.items():
            if table not in tables:
                print("INSUFFICIENT=1")
                return
            c.execute(f"PRAGMA table_info({table})")
            actual_cols = {row[1] for row in c.fetchall()}
            if not set(cols).issubset(actual_cols):
                print("INSUFFICIENT=1")
                return
        
        # The hypothesis requires IPO data, lockup expiry dates, 
        # earnings schedules, corporate events, book equity, etc.
        # None of these exist in the provided schema.
        print("INSUFFICIENT=1")
        
    except Exception:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()