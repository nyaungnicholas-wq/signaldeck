#!/usr/bin/env python3
"""Generate a machine-readable inventory of the SignalDeck SQLite database."""

import sqlite3
import sys
from datetime import datetime, timedelta, timezone

TABLE_TIME_COLS = {
    "bars": "ts",
    "symbols": "added_at",
    "prediction_outcomes": "ts",
    "insider_trades": "filed_ts",
    "filings": "filed_ts",
    "short_volume": "day",
    "news": "ts",
    "sentiment_features": "day",
    "stocktwits_sentiment": "ts",
    "macro_series": "ts",
    "fundamentals": "fetched_at",
    "inst_holdings": "period",
    "anomalies": "ts",
}

EPOCH_COLUMNS = {
    "bars", "symbols", "prediction_outcomes", "insider_trades", "filings",
    "news", "stocktwits_sentiment", "macro_series", "fundamentals", "anomalies"
}

def epoch_to_date(v):
    return (datetime(1970, 1, 1, tzinfo=timezone.utc) + timedelta(seconds=v)).date().isoformat()

def main():
    db_path = "data/signaldeck.db"
    args = sys.argv[1:]
    if args:
        if args[0] == "--db" and len(args) == 2:
            db_path = args[1]
        else:
            print("Usage: python ops/schema_inventory.py [--db PATH]", file=sys.stderr)
            sys.exit(1)

    conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    cursor = conn.cursor()

    print("SCHEMA INVENTORY")
    for table, time_col in TABLE_TIME_COLS.items():
        cols = [row[1] for row in cursor.execute(f"PRAGMA table_info({table})")]
        col_list = ", ".join(cols)

        count = cursor.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0]
        count_str = f"{count:,}"

        symbol_part = ""
        if "symbol_id" in cols:
            sym_count = cursor.execute(f"SELECT COUNT(DISTINCT symbol_id) FROM {table}").fetchone()[0]
            symbol_part = f", {sym_count:,} symbols"

        range_part = ""
        if time_col in cols:
            min_val, max_val = cursor.execute(
                f"SELECT MIN({time_col}), MAX({time_col}) FROM {table}"
            ).fetchone()
            if min_val is not None and max_val is not None:
                if table in EPOCH_COLUMNS:
                    min_dt = epoch_to_date(min_val)
                    max_dt = epoch_to_date(max_val)
                else:
                    min_dt = str(min_val)[:10]
                    max_dt = str(max_val)[:10]
                range_part = f"  -- {count_str} rows{symbol_part}, {min_dt}..{max_dt}."
            else:
                range_part = f"  -- {count_str} rows{symbol_part}, (empty)."
        else:
            range_part = f"  -- {count_str} rows{symbol_part}."

        print(f"- {table}({col_list}){range_part}")

        if table == "prediction_outcomes":
            horizons = cursor.execute(
                "SELECT horizon, COUNT(*) FROM prediction_outcomes GROUP BY horizon ORDER BY horizon"
            ).fetchall()
            horizon_str = ", ".join(f"{h} ({c:,})" for h, c in horizons)
            label_days = cursor.execute(
                "SELECT COUNT(DISTINCT date(ts, 'unixepoch')) FROM prediction_outcomes"
            ).fetchone()[0]
            print(f"    available horizons: {horizon_str}")
            print(f"    distinct label days: {label_days}")
            print("    A hypothesis HORIZON must be one of the horizons above. A backtest window")
            print("    must fit inside the date range above.")

    print("# END SCHEMA INVENTORY")
    conn.close()

if __name__ == "__main__":
    main()