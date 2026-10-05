import sqlite3
import math
import datetime

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    db.row_factory = sqlite3.Row
    cur = db.cursor()
    
    # Get all bars and stocktwits, joining on symbol and date
    # Convert ts to date string for proper grouping
    query = """
    WITH bars_daily AS (
        SELECT symbol_id,
               date(ts, 'unixepoch') as day,
               close
        FROM bars
        WHERE tf = '1d'
    ),
    st_daily AS (
        SELECT symbol_id,
               date(ts, 'unixepoch') as day,
               SUM(bull) as bull,
               SUM(bear) as bear,
               SUM(bull + bear) as total_messages
        FROM stocktwits_sentiment
        GROUP BY symbol_id, date(ts, 'unixepoch')
    ),
    combined AS (
        SELECT 
            b.symbol_id,
            b.day,
            b.close,
            s.bull,
            s.bear,
            s.total_messages
        FROM bars_daily b
        JOIN st_daily s ON b.symbol_id = s.symbol_id AND b.day = s.day
    )
    SELECT * FROM combined
    ORDER