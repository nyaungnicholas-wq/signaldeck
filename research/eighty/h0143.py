import sqlite3

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # Check if we have the necessary tables
        c.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = set(row[0] for row in c.fetchall())
        required = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
        if not required.issubset(tables):
            print("INSUFFICIENT=1")
            return
        
        # Check for rating downgrade data - we need a table with rating actions
        # The provided tables do not contain rating agency actions, so we cannot
        # identify downgrade events. We must abort.
        print("INSUFFICIENT=1")
        return
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        try:
            conn.close()
        except:
            pass

if __name__ == "__main__":
    main()