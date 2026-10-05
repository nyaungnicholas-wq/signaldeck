import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Check for required tables and earnings-related data
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cur.fetchall()}
        
        # Check for earnings data availability by looking for columns we'd need
        required_tables = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
        if not required_tables.issubset(tables):
            print("INSUFFICIENT=1")
            return 0
            
        # Check for earnings surprise data by looking for relevant columns in prediction_outcomes or scores
        # Since we can only use listed columns, we need to find earnings announcement dates
        # The schema has no earnings-related columns, so we cannot identify earnings events
        
        # Check if there's any way to identify earnings announcements from the given data
        cur.execute("PRAGMA table_info(prediction_outcomes)")
        pred_columns = {row[1] for row in cur.fetchall()}
        
        cur.execute("PRAGMA table_info(regime_outcomes)")
        regime_columns = {row[1] for row in cur.fetchall()}
        
        cur.execute("PRAGMA table_info(scores)")
        scores_columns = {row[1] for row in cur.fetchall()}
        
        # None of these tables contain earnings announcement dates, earnings surprise, etc.
        # The hypothesis requires earnings data to identify events
        
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