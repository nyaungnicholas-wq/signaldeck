import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Check if we have necessary tables and enough data
        cursor.execute("SELECT COUNT(*) FROM bars")
        bars_count = cursor.fetchone()[0]
        
        cursor.execute("SELECT COUNT(*) FROM symbols")
        symbols_count = cursor.fetchone()[0]
        
        if bars_count < 100000 or symbols_count < 100:
            print("INSUFFICIENT=1")
            return 0
        
        # The hypothesis requires earnings announcement dates and SUE scores
        # but these columns don't exist in the provided schema
        # We cannot implement the hypothesis without earnings data
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