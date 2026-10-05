#!/usr/bin/env python3
import sqlite3
import sys

REQUIRED = {
    "bars": {"symbol_id", "tf", "ts", "open", "high", "low", "close", "volume"},
    "symbols": {"id", "symbol", "market", "name", "active", "added_at", "stream", "delisted_at"},
    "regime_outcomes": {"id", "symbol_id", "kind", "ts", "day", "horizon_days", "regime", "conviction", "historical_accuracy", "rank", "resolved_at", "actual", "correct", "naive_label", "revision", "basis_epoch"},
    "prediction_outcomes": {"symbol_id", "horizon", "ts", "prob", "up", "fwd_return", "resolved_at", "basis_epoch"},
    "scores": {"symbol_id", "horizon", "ts", "score", "components"},
}

def main():
    try:
        db = sqlite3.connect("file:data/signaldeck.db?mode=ro", uri=True)
    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return 0

    for table, required in REQUIRED.items():
        try:
            cols = {row[1] for row in db.execute(f"PRAGMA table_info({table})")}
        except sqlite3.Error:
            print("INSUFFICIENT=1")
            return 0
        if not required.issubset(cols):
            print("INSUFFICIENT=1")
            return 0

    try:
        kinds = [row[0] for row in db.execute("SELECT DISTINCT kind FROM regime_outcomes") if row[0] is not None]
        streams = [row[0] for row in db.execute("SELECT DISTINCT stream FROM symbols") if row[0] is not None]
    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return 0

    markers = ("russell", "reconstitut", "membership", "index_add")
    found = False
    for value in kinds:
        if any(marker in str(value).lower() for marker in markers):
            found = True
            break
    if not found:
        for value in streams:
            if any(marker in str(value).lower() for marker in markers):
                found = True
                break

    if not found:
        print("INSUFFICIENT=1")
        return 0

    # A marker alone is not enough: the schema has no column identifying the
    # announcement date A or the complete list of newly added Russell 2000 names.
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())