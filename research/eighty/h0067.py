import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Check available tables
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cursor.fetchall()}
        
        required_tables = {'bars', 'symbols', 'prediction_outcomes'}
        if not required_tables.issubset(tables):
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # Check for required columns
        cursor.execute("PRAGMA table_info(symbols)")
        symbols_cols = {row[1] for row in cursor.fetchall()}
        if 'market' not in symbols_cols:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # Check if we have enough symbols with market='stocks'
        cursor.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks' AND active=1")
        stock_count = cursor.fetchone()[0]
        if stock_count < 10:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # Check if we have enough bars data
        cursor.execute("SELECT COUNT(*) FROM bars WHERE tf='1d'")
        bar_count = cursor.fetchone()[0]
        if bar_count < 100000:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # Check for prediction_outcomes labels
        cursor.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE up IS NOT NULL")
        label_count = cursor.fetchone()[0]
        if label_count < 1000:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # Cannot implement full hypothesis due to missing data:
        # - No market cap, book equity, earnings schedule, news events
        # - No 52-week high data directly available
        # - No intraday range for top-half condition
        # - No event data for abstention conditions
        # Print INSUFFICIENT=1 as required
        print("INSUFFICIENT=1")
        conn.close()
        
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)

if __name__ == "__main__":
    main()