"""bars(1d) -> panel.parquet. Survivorship-complete: keeps delisted symbols."""
import sqlite3, pandas as pd
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
FUND_PAT = (r"ETF|ETN|Fund|ProShares|Direxion|iShares|SPDR|Invesco|Vanguard|"
            r"UltraShort|UltraPro|Ultra|Bear|Bull|[23]X|"
            r"Leveraged|Select Sector|Daily Target|Index Trust")
before = df.symbol_id.nunique()
df = df[~df["name"].fillna("").str.contains(FUND_PAT, case=False, regex=True)]
print("fund filter: %d -> %d symbols (%d fund/leveraged dropped)"
      % (before, df.symbol_id.nunique(), before - df.symbol_id.nunique()))

df = df[df.close > 0].sort_values(["symbol_id", "day"]).reset_index(drop=True)
df.to_parquet("panel.parquet", index=False)
print("rows", len(df), "symbols", df.symbol_id.nunique(),
      "days", df.day.nunique(), df.day.min().date(), df.day.max().date())
print("delisted symbols present:", df[df.delisted_at.notna()].symbol_id.nunique())
