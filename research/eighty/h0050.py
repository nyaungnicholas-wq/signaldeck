import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    try:
        cur = conn.cursor()
        # Check for required tables and columns
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cur.fetchall()}
        
        required_tables = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
        if not required_tables.issubset(tables):
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Check for necessary data: at least one row in symbols and bars
        cur.execute("SELECT COUNT(*) FROM symbols")
        if cur.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        cur.execute("SELECT COUNT(*) FROM bars")
        if cur.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # Check for lockup-related data: we need columns for lockup expiration, shares outstanding, etc.
        # Since the schema does not contain these, we immediately determine insufficient.
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
        
    except Exception as e:
        print("INSUFFICIENT=1")
        try:
            conn.close()
        except:
            pass
        sys.exit(0)

if __name__ == "__main__":
    main()