import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Check for rating action tables/columns we need
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cursor.fetchall()}
        
        # We need rating actions - none exist in the schema
        rating_tables = {'rating_actions', 'credit_ratings', 'rating_changes', 'ratings'}
        if not rating_tables.intersection(tables):
            print("INSUFFICIENT=1")
            return 0
            
        # We need corporate announcements - none exist
        announcement_tables = {'announcements', 'corporate_actions', 'events'}
        if not announcement_tables.intersection(tables):
            print("INSUFFICIENT=1")
            return 0
            
        # We need earnings calendar - none exist
        earnings_tables = {'earnings', 'earnings_calendar', 'earnings_dates'}
        if not earnings_tables.intersection(tables):
            print("INSUFFICIENT=1")
            return 0
            
        # We need fundamental data (book equity, going-concern)
        fundamental_tables = {'fundamentals', 'financials', 'balance_sheets'}
        if not fundamental_tables.intersection(tables):
            print("INSUFFICIENT=1")
            return 0
            
        # We need to check columns exist
        required_rating_cols = {'symbol', 'action', 'notch_change', 'agency', 'date'}
        cursor.execute("PRAGMA table_info(rating_actions)")
        rating_cols = {row[1] for row in cursor.fetchall()}
        if not required_rating_cols.issubset(rating_cols):
            print("INSUFFICIENT=1")
            return 0
            
        print("INSUFFICIENT=1")
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0

if __name__ == "__main__":
    sys.exit(main())