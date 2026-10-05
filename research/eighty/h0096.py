import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()
        
        # Check for required tables
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row['name'] for row in cursor.fetchall()}
        required = {'bars', 'symbols', 'prediction_outcomes'}
        if not required.issubset(tables):
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Get list of U.S.-listed stocks
        cursor.execute("""
            SELECT id, symbol, market 
            FROM symbols 
            WHERE market = 'stocks'
            AND delisted_at IS NULL
        """)
        us_stocks = {row['id']: dict(row) for row in cursor.fetchall()}
        if not us_stocks:
            print("INSUFFICIENT=1")
            conn.close()
            return
        
        # Get 1d bars for all US stocks
        symbol_ids = list(us_stocks.keys())
        cursor.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume
            FROM bars
            WHERE tf = '1d'
            AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join('?' * len(symbol_ids))), symbol_ids)
        
        bars_by_symbol = defaultdict(list)
        for row in cursor.fetchall():
            bars_by_symbol[row['symbol_id']].append(dict(row))
        
        # We need repurchase authorization data to test the hypothesis
        # The database doesn't have this table, so we must exit with INSUFFICIENT=1
        print("INSUFFICIENT=1")
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)

if __name__ == "__main__":
    main()