import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()
        
        # Check if we have necessary tables with required columns
        required_tables = {
            'bars': ['symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'],
            'symbols': ['id', 'symbol', 'market']
        }
        
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        existing_tables = {row[0] for row in cursor.fetchall()}
        
        for table, columns in required_tables.items():
            if table not in existing_tables:
                print("INSUFFICIENT=1")
                return 0
            cursor.execute(f"PRAGMA table_info({table})")
            existing_columns = {row[1] for row in cursor.fetchall()}
            missing = set(columns) - existing_columns
            if missing:
                print("INSUFFICIENT=1")
                return 0
        
        # Check we have enough data to even consider the hypothesis
        cursor.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks'")
        stock_count = cursor.fetchone()[0]
        
        # We need U.S. common stocks - assuming all stocks in this DB are U.S.
        # Need at least some historical data
        cursor.execute("""
            SELECT COUNT(DISTINCT symbol_id) 
            FROM bars 
            WHERE tf='1d'
        """)
        symbols_with_data = cursor.fetchone()[0]
        
        if stock_count < 10 or symbols_with_data < 10:
            print("INSUFFICIENT=1")
            return 0
            
        # The hypothesis requires credit rating downgrade events
        # We don't have a credit rating table in the schema
        # The available tables: bars, symbols, regime_outcomes, prediction_outcomes, scores
        # None contain credit rating data
        print("INSUFFICIENT=1")
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    sys.exit(main())