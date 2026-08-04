#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict
from datetime import datetime

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.row_factory = sqlite3.Row
        c = conn.cursor()
        
        # Check if we have the required tables and columns
        c.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = [row[0] for row in c.fetchall()]
        required = ['bars', 'symbols', 'prediction_outcomes']
        for t in required:
            if t not in tables:
                print("INSUFFICIENT=1")
                return 0
        
        # Check we have the needed columns in prediction_outcomes
        c.execute("PRAGMA table_info(prediction_outcomes)")
        po_cols = {row[1] for row in c.fetchall()}
        needed_po = {'symbol_id', 'horizon', 'ts', 'prob', 'up', 'fwd_return', 'resolved_at', 'basis_epoch'}
        if not needed_po.issubset(po_cols):
            print("INSUFFICIENT=1")
            return 0
        
        # Check we have bars and symbols columns
        c.execute("PRAGMA table_info(bars)")
        bar_cols = {row[1] for row in c.fetchall()}
        needed_bars = {'symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'}
        if not needed_bars.issubset(bar_cols):
            print("INSUFFICIENT=1")
            return 0
            
        c.execute("PRAGMA table_info(symbols)")
        sym_cols = {row[1] for row in c.fetchall()}
        needed_sym = {'id', 'symbol', 'market'}
        if not needed_sym.issubset(sym_cols):
            print("INSUFFICIENT=1")
            return 0
        
        # We need earnings surprise data which isn't in the database schema.
        # The hypothesis requires earnings consensus and EPS data, but our tables 
        # contain only bars, symbols, regime_outcomes, prediction_outcomes, and scores.
        # Without earnings data we cannot compute entry conditions.
        print("INSUFFICIENT=1")
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0

if __name__ == '__main__':
    main()