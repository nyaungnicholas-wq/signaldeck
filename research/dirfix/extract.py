"""bars(1d) -> panel.parquet. Survivorship-complete: keeps delisted symbols."""
import sqlite3, pandas as pd
DB = "file:../../data/signaldeck.db?mode=ro"
c = sqlite3.connect(DB, uri=True)
sym = pd.read_sql("select id symbol_id, symbol, market, delisted_at from symbols", c)
df = pd.read_sql(
    "select symbol_id, ts, open, high, low, close, volume from bars where tf='1d'", c)
df["day"] = pd.to_datetime(df.ts, unit="s", utc=True).dt.tz_localize(None).dt.normalize()
df = df.merge(sym, on="symbol_id", how="left")
# STOCKS ONLY. 7 crypto symbols (5,302 rows) trade 7 days a week; mixing them
# in adds 1,515 weekend rows to the shared day index on which every stock is
# NaN, so universe_mask's "252 days of prior history" became unsatisfiable for
# every stock from 2024-07-11 on. Calendar contamination from 0.24% of rows.
df = df[df.market == "stocks"]
df = df[df.close > 0].sort_values(["symbol_id", "day"]).reset_index(drop=True)
df.to_parquet("panel.parquet", index=False)
print("rows", len(df), "symbols", df.symbol_id.nunique(),
      "days", df.day.nunique(), df.day.min().date(), df.day.max().date())
print("delisted symbols present:", df[df.delisted_at.notna()].symbol_id.nunique())
