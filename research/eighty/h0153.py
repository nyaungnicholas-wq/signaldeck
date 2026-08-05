import sqlite3

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Check if we have the required tables
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    required = {'bars', 'symbols', 'prediction_outcomes'}
    if not required.issubset(tables):
        print("INSUFFICIENT=1")
        return
    
    # Check columns in symbols
    cur.execute("PRAGMA table_info(symbols)")
    sym_cols = {row[1] for row in cur.fetchall()}
    required_sym_cols = {'id', 'symbol', 'market'}
    if not required_sym_cols.issubset(sym_cols):
        print("INSUFFICIENT=1")
        return
    
    # Check columns in bars
    cur.execute("PRAGMA table_info(bars)")
    bar_cols = {row[1] for row in cur.fetchall()}
    required_bar_cols = {'symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'}
    if not required_bar_cols.issubset(bar_cols):
        print("INSUFFICIENT=1")
        return
    
    # Check columns in prediction_outcomes
    cur.execute("PRAGMA table_info(prediction_outcomes)")
    pred_cols = {row[1] for row in cur.fetchall()}
    required_pred_cols = {'symbol_id', 'horizon', 'ts', 'prob', 'up', 'fwd_return', 'resolved_at', 'basis_epoch'}
    if not required_pred_cols.issubset(pred_cols):
        print("INSUFFICIENT=1")
        return
    
    # We need insider transaction data to test the hypothesis, but none exists in the database.
    # The hypothesis requires Form 4 filings, insider purchase details, etc.
    # The available tables do not contain this information.
    print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()