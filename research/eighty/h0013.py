import sqlite3, sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    available = {row[0] for row in cur.fetchall()}
    if not {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}.issubset(available):
        print("INSUFFICIENT=1")
        sys.exit(0)
    cur.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks' AND active=1")
    stock_count = cur.fetchone()[0]
    cur.execute("SELECT COUNT(DISTINCT symbol_id) FROM prediction_outcomes")
    pred_sym_count = cur.fetchone()[0]
    if stock_count == 0 or pred_sym_count == 0:
        print("INSUFFICIENT=1")
        sys.exit(0)
    # No dividend announcements table exists in schema; cannot identify dividend initiations.
    print("INSUFFICIENT=1")
    sys.exit(0)
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)