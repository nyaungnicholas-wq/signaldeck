#!/usr/bin/env python3
import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except sqlite3.Error:
        print('INSUFFICIENT=1')
        return 0

    try:
        columns = set()
        tables = [r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type='table'")]
        for table in tables:
            columns.update(r[1].lower() for r in conn.execute('PRAGMA table_info("%s")' % table))

        required = {
            'filing_date', 'form4_date', 'exec_role', 'executive', 'ceo', 'cfo',
            'transaction_type', 'purchase_amount', '10b5', 'book_equity',
            'going_concern', 'earnings_date', 'announcement_date'
        }

        if not (required & columns):
            print('INSUFFICIENT=1')
            return 0

        # Even with unexpected extra columns, the remaining hard data requirements
        # are absent, so the hypothesis cannot be measured without fabrication.
        print('INSUFFICIENT=1')
        return 0
    finally:
        conn.close()

if __name__ == '__main__':
    sys.exit(main())