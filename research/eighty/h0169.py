#!/usr/bin/env python3
"""Test follow-on equity offering hypothesis using read-only signaldeck.db."""

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timezone

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception:
        print("INSUFFICIENT=1")
        return

    # Check what data we actually have
    try:
        # First, verify that we have the necessary tables
        required_tables = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
        cursor = conn.cursor()
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        actual_tables = {row[0] for row in cursor.fetchall()}
        if not required_tables.issubset(actual_tables):
            print("INSUFFICIENT=1")
            return

        # Check if there are any US stocks in symbols
        cursor.execute("SELECT COUNT(*) FROM symbols WHERE market = 'stocks'")
        stock_count = cursor.fetchone()[0]
        if stock_count == 0:
            print("INSUFFICIENT=1")
            return

        # Check if we have enough bars data
        cursor.execute("SELECT COUNT(*) FROM bars WHERE tf = '1d'")
        daily_bars_count = cursor.fetchone()[0]
        if daily_bars_count < 10000:  # Need reasonable amount
            print("INSUFFICIENT=1")
            return

        # Check symbols with 12+ months history
        cursor.execute("""
            SELECT COUNT(DISTINCT s.id) 
            FROM symbols s
            JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
            WHERE s.market = 'stocks'
            GROUP BY s.id
            HAVING COUNT(DISTINCT b.ts) >= 252  # ~12 months of trading days
        """)
        sufficient_history = cursor.fetchone()
        if not sufficient_history or sufficient_history[0] < 10:
            print("INSUFFICIENT=1")
            return

        # We don't have offering announcements, so cannot construct universe
        # This script cannot test the hypothesis without that data
        print("INSUFFICIENT=1")

    except Exception:
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()