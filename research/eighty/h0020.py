import sqlite3
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    try:
        cur = conn.cursor()

        # Verify the documented tables exist.
        tables = {row[0] for row in cur.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        )}
        required = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
        if not required.issubset(tables):
            print('INSUFFICIENT=1')
            return

        # The strategy requires Form 4 insider-filing data.  None of the
        # documented tables contain filing, owner, transaction, or plan fields.
        columns = set()
        for table in tables:
            columns.update(row[1].lower() for row in cur.execute(
                f'PRAGMA table_info("{table}")'
            ))

        needed_terms = (
            'form4', 'filing_date', 'transaction_date', 'owner', 'relationship',
            'transaction_type', 'shares', 'purchase', '10b5', '13d', '13g',
            'buyback', 'offering'
        )
        if not any(term in col for col in columns for term in needed_terms):
            print('INSUFFICIENT=1')
            return

        # No documented schema is available to implement the exact entry and
        # abstain rules without fabricating columns.  Do not fabricate.
        print('INSUFFICIENT=1')
    finally:
        conn.close()

if __name__ == '__main__':
    main()