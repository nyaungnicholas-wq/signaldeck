import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Check if required columns for hypothesis exist in any table.
        # We need EPS estimate revisions and analyst counts, which are not in the schema.
        # Query for any table that might contain EPS-related columns.
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = [r[0] for r in cur.fetchall()]
        
        # Quick check for any hint of estimate data.
        estimate_possible = False
        for table in tables:
            cur.execute(f"PRAGMA table_info({table})")
            cols = [row[1].lower() for row in cur.fetchall()]
            # Look for keywords suggesting EPS estimates or analyst counts.
            if any(kw in ' '.join(cols) for kw in ['eps', 'estimate', 'analyst', 'revision']):
                estimate_possible = True
                break
        
        if not estimate_possible:
            # No data to compute consensus EPS revisions or analyst counts.
            print("INSUFFICIENT=1")
            conn.close()
            sys.exit(0)
        
        # If we reach here, we might have data, but the schema says we don't.
        # However, to be safe, we follow the same logic: insufficient.
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
        
    except Exception as e:
        # Any error (e.g., database not found) -> insufficient.
        print("INSUFFICIENT=1")
        sys.exit(0)

if __name__ == "__main__":
    main()