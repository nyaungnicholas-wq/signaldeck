"""bars(1d) -> panel.parquet. Survivorship-complete: keeps delisted symbols."""
import sqlite3, sys, pandas as pd
from collections import Counter
from pathlib import Path
DB = "file:../../data/signaldeck.db?mode=ro"
c = sqlite3.connect(DB, uri=True)
sym = pd.read_sql("select id symbol_id, symbol, market, name, delisted_at from symbols", c)
df = pd.read_sql(
    "select symbol_id, ts, open, high, low, close, volume from bars where tf='1d'", c)
df["day"] = pd.to_datetime(df.ts, unit="s", utc=True).dt.tz_localize(None).dt.normalize()
df = df.merge(sym, on="symbol_id", how="left")
# STOCKS ONLY. 7 crypto symbols (5,302 rows) trade 7 days a week; mixing them
# in adds 1,515 weekend rows to the shared day index on which every stock is
# NaN, so universe_mask's "252 days of prior history" became unsatisfiable for
# every stock from 2024-07-11 on. Calendar contamination from 0.24% of rows.
df = df[df.market == "stocks"]
# EXCLUDE FUNDS. `market='stocks'` does NOT exclude ETFs in this DB: LQD, TLT,
# SPY and leveraged inverse products (TSLZ -2x TSLA, MSTZ -2x MSTR, SOXS -3x
# semis) are all filed as stocks. Measured 2026-08-15 they were 52.7% of long
# picks and 51.2% of SHORT picks, and shorting them harvests daily-rebalance
# DECAY rather than forecasting anything -- at 20-100%/yr borrow against the 3%
# the backtest charged.
#
# Filtered on NAME, deliberately not on `fundamentals` presence: that table
# covers only currently-subscribed live names, so using it cut delisted symbols
# from 1,868 to 1 and replaced a fund-contamination bias with a far worse
# survivorship one. Bare "Shares"/"Trust"/"Index" are also excluded from the
# pattern -- they match ADRs ("American Depositary Shares"), foreign issuers
# ("Ordinary Shares") and REITs ("QTS REALTY TRUST"), which are real companies.
# The name regex alone LEAKED. Measured 2026-08-29 it passed 353 non-common-
# equity instruments -- 13.1% of survivors: 254 SPAC units, 58 warrants, 12
# rights, 18 preferred/notes, 11 closed-end muni funds. Those are option-like,
# price-pinned or interest-rate instruments and they distort every
# cross-sectional statistic. Extending the regex is NOT the fix -- it
# false-positives on real companies (AARD, CORE, CYBR, AMWD, XEC) -- so
# classification now runs on the exchange ticker-suffix convention, which is
# deterministic rather than lexical. See research/harness/instruments.py.
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "harness"))
from instruments import instrument_class  # noqa: E402

before = df.symbol_id.nunique()
klass = [instrument_class(s, n) for s, n in zip(df["symbol"], df["name"])]
# Count DISTINCT SYMBOLS, not rows -- klass is per-row, so counting it
# directly reports bar counts and reads like a far bigger cull than it is.
dropped = Counter(k for _, k in set(zip(df["symbol_id"], klass)) if k)
df = df[[k is None for k in klass]]
print("instrument filter: %d -> %d symbols (%d non-equity dropped: %s)"
      % (before, df.symbol_id.nunique(), before - df.symbol_id.nunique(),
         ", ".join("%s=%d" % kv for kv in dropped.most_common())))

df = df[df.close > 0].sort_values(["symbol_id", "day"]).reset_index(drop=True)
df.to_parquet("panel.parquet", index=False)
print("rows", len(df), "symbols", df.symbol_id.nunique(),
      "days", df.day.nunique(), df.day.min().date(), df.day.max().date())
print("delisted symbols present:", df[df.delisted_at.notna()].symbol_id.nunique())
