import sqlite3
import sys

def main():
    conn = None
    try:
        conn = sqlite3.connect("file:data/signaldeck.db?mode=ro", uri=True)
        cur = conn.cursor()

        tables = {row[0] for row in cur.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        )}
        required_tables = {
            "bars", "symbols", "regime_outcomes", "prediction_outcomes", "scores"
        }
        if not required_tables.issubset(tables):
            print("INSUFFICIENT=1")
            return 0

        required_columns = {
            "bars": {"symbol_id", "tf", "ts", "open", "high", "low", "close", "volume"},
            "symbols": {"id", "symbol", "market"},
            "regime_outcomes": {
                "id", "symbol_id", "kind", "ts", "day", "horizon_days",
                "regime", "conviction", "historical_accuracy", "rank",
                "resolved_at", "actual", "correct", "naive_label",
                "revision", "basis_epoch",
            },
            "prediction_outcomes": {
                "symbol_id", "horizon", "ts", "prob", "up",
                "fwd_return", "resolved_at", "basis_epoch",
            },
            "scores": {"symbol_id", "horizon", "ts", "score", "components"},
        }

        for table, columns in required_columns.items():
            actual = {row[1] for row in cur.execute(f'PRAGMA table_info("{table}")')}
            if not columns.issubset(actual):
                print("INSUFFICIENT=1")
                return 0

        # The schema contains no ASR announcement source and no announcement
        # attributes: no ASR value, funding mechanism, concurrent event flags,
        # 90-day buyback history, book equity, or going-concern status.
        # Testing the stated hypothesis would require fabricating inputs.
        print("INSUFFICIENT=1")
        return 0

    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return 0
    finally:
        if conn is not None:
            conn.close()

if __name__ == "__main__":
    sys.exit(main())