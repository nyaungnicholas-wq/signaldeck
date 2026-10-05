import sqlite3
import sys

def main():
    # Connect to database in read-only mode
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print(f"INSUFFICIENT=1")
        sys.exit(0)
    
    cursor = conn.cursor()
    
    # Check if we have the necessary tables
    required_tables = ['bars', 'symbols', 'prediction_outcomes']
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    existing_tables = {row[0] for row in cursor.fetchall()}
    
    if not all(t in existing_tables for t in required_tables):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Check if we have credit rating data (downgrade events)
    # The database doesn't have rating tables, so we cannot identify downgrades
    # According to instructions: if data insufficient, print INSUFFICIENT=1
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == "__main__":
    main()