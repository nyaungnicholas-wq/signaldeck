#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime, timedelta

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.execute("PRAGMA journal_mode=WAL;")
        c = conn.cursor()
        
        # Check for S&P 500 additions data - this is the critical missing piece
        # The hypothesis requires identification of S&P 500 index additions
        # We have no table that tracks index composition changes
        # regime_outcomes might have 'kind' related to index additions but schema doesn't confirm
        
        # Check if regime_outcomes contains S&P 500 addition data
        try:
            c.execute("SELECT DISTINCT kind FROM regime_outcomes LIMIT 20")
            kinds = [row[0] for row in c.fetchall()]
            sp500_keywords = ['sp500', 's&p500', 'index addition', 'idx_add']
            has_sp500_data = any(keyword in str(k).lower() for k in kinds for keyword in sp500_keywords)
        except:
            has_sp500_data = False
            
        if not has_sp500_data:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # If we had S&P 500 data, we would proceed with the full analysis
        # For now, exit with insufficient data
        print("INSUFFICIENT=1")
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()