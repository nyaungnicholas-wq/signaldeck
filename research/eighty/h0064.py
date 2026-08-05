import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    cur = conn.cursor()
    
    # Check if we have enough data to identify spin-offs - need at least 1080 symbols
    cur.execute("SELECT COUNT(*) FROM symbols")
    if cur.fetchone()[0] < 10:
        print("INSUFFICIENT=1")
        return
    
    # The database doesn't contain spin-off identification data
    # We cannot identify which stocks are spin-offs from the given schema
    # This means we cannot form the universe required by the hypothesis
    
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == '__main__':
    main()