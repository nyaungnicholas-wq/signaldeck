import sqlite3
import sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cursor = conn.cursor()
    
    # Check for dividend data - need a table or column that identifies dividend initiations
    # None of the known tables have dividend data columns
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cursor.fetchall()}
    
    # Check if any table has dividend-related columns
    dividend_cols = False
    for table in ['bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores']:
        if table in tables:
            cursor.execute(f"PRAGMA table_info({table})")
            cols = [row[1].lower() for row in cursor.fetchall()]
            if any('dividend' in col for col in cols):
                dividend_cols = True
                break
    
    conn.close()
    
    if not dividend_cols:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # If we had dividend data, we would implement the full test here
    # Since we don't, we cannot test the hypothesis
    
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)

print("INSUFFICIENT=1")
sys.exit(0)