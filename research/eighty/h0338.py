# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 337
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    try:
        conn = sqlite3.connect(db_path, uri=True)
    except:
        print("INSUFFICIENT=1")
        return
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check if required columns exist in insider_trades
    cur.execute("PRAGMA table_info(insider_trades)")
    cols = {row[1] for row in cur.fetchall()}
    needed = {'accession','symbol_id','insider','title','code','shares','price','value','tx_ts','filed_ts'}
    if not needed.issubset(cols):
        print("INSUFFICIENT=1")
        return

    # Verify we have enough daily bars for the 20-day lookback and 21-day horizon
    # First, get all symbols that have insider trades
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    insider_symbol_ids = {row[0] for row in cur.fetchall()}
    if not insider_symbol_ids:
        print("INSUFFICIENT=1")
        return

    # Get all 1d bars for these symbols
    # We need to compute disclosure dates and check for 6 months of daily bars before disclosure date
    # Disclosure date is filed_ts (converted to date)
    # We will compute for each insider trade if it meets the entry criteria and then look forward 21 trading days

    # We must respect as-of discipline: at decision time (first trading day after disclosure date) we can only use info up to that day.
    # The disclosure date is filed_ts. The decision date is the next trading day after filed_ts.
    # We need to know the next trading day after filed_ts from the bars table.
    # We'll use the bars table to determine trading days.

    # We'll fetch all 1d bars for the insider symbols, sorted by symbol and ts
    # Since there are 13.2M rows but we only need a subset, we fetch in chunks
    # We'll build a mapping of (symbol_id, date) -> (close, volume, next_trading_day, 20-day median dollar volume)
    # However, building full mapping may be memory heavy. We'll process per symbol.

    # We need to compute for each insider trade:
    # 1. Disclosure date = filed_ts (as date)
    # 2. Check if trade code is 'M' (option exercise) and that the trade is an exercise (code M) and that the insider retained shares (we don't have post-transaction shares held, only transaction shares)
    #    The schema does not have pre/post transaction shares. The hypothesis requires post-transaction shares held > pre-transaction shares held.
    #    Since we don't have that data, we cannot implement this condition. We must output INSUFFICIENT=1.

    # Wait: The hypothesis says: "the filer's post-transaction shares held are strictly greater than pre-transaction shares held"
    # The insider_trades table does not contain pre- or post-transaction shares held. It only contains the transaction shares.
    # Therefore, we cannot determine if shares were retained. We must output INSUFFICIENT=1.

    # However, let's double-check the schema block for insider_trades: it lists only the columns above. There is no column for holdings.
    # So we cannot implement the mechanism as described.

    print("INSUFFICIENT=1")
    return

if __name__ == "__main__":
    main()