# SIGNAL8 WAVE — "Filings in. Signals out." for SignalDeck

Goal: copy signal8.ai's feature set (user request) into SignalDeck using 100% FREE
sources, and add the user's explicit asks: **trade-imbalance detection** and
**unusual-volatility/volume detection**. All under the honesty doctrine.

## What signal8.ai is (scraped 2026-07-04, homepage + search)
- Tagline: "Filings in. Signals out." — SEC filing intelligence platform.
- Homepage layout (top→bottom):
  1. **Index ticker tape**: S&P, NASDAQ-100, Dow, Russell, VIX, sector ETFs
     (Tech/Financials/Energy/Health), Gold/Silver/Oil, 20Y Treasury, Semis, BTC,
     Real Estate — price + day % chg, each linking to a detail page.
  2. **Top Stories** (news cards w/ ticker chips) + **Latest News** feed that
     mixes press/news with **SEC filings rendered in plain English**
     ("SEM: S-8 POS — acquisition merger", "BBBY: 8-K — acquisition merger",
     source tag `sec-filing`).
  3. **Earnings calendar** (ticker/company/est/prev), **Economic calendar**
     (NFP, ISM w/ est+prev), **IPO calendar** (upcoming listings + range).
  4. **Top Gainers/Losers** with market-cap filter (e.g. "Over $10B") —
     ticker/name/price/chg%/mcap.
- Other sections (from search + site map): **insider trades** (Form 4),
  **institutional flow** (13F), **dilution risk**, **congressional/politician
  trades**, **companies directory** (price, mcap, sector, dilution risk,
  financials, insider + institutional holdings per company).

## Free data mapping (never scrape signal8 itself; their data/API is theirs)
| Feature | Free source |
|---|---|
| Insider trades | SEC EDGAR Form 3/4/5 (data.sec.gov, free, UA header, ≤10 req/s) |
| Institutional flow | SEC EDGAR 13F-HR filings |
| Dilution risk | EDGAR: S-1/S-3/424B filings + shares-outstanding deltas from companyfacts (Stage-2 EDGAR ingest already pulls companyfacts) |
| Filings feed (plain English) | EDGAR full-index / submissions API; map form types → human labels (8-K item codes, S-8, 13D/G, 10-Q/K…) |
| Congressional trades | Senate eFD + House FD public disclosures (free mirrors: senatestockwatcher.com / housestockwatcher.com JSON; degrade gracefully) |
| Econ calendar | FRED release calendar (Stage-2 fred ingest) — honest subset; label coverage |
| Earnings calendar | Alpaca corporate-actions/earnings if available on free tier, else EDGAR 10-Q/K due-date heuristic, honestly labeled |
| Ticker tape | Existing bars for SPY/QQQ/DIA/IWM + sector ETFs (add to daily universe if missing) + BTC (TickStream) + VIX (FRED VIXCLS) |
| Movers | Existing discovery most-actives/movers sweep (candidates table) + universe daily bars |

## The user's explicit asks (anomaly layer)
New package `internal/anomaly`, worker + page + alert kinds:
1. **Trade imbalance**: crypto — rolling z-score of order-book imbalance from
   snapshots_1s (|z|≥2.5 → "unusual buy/sell pressure"); stocks — volume-side
   proxy: up-volume vs down-volume ratio z-score on 1m bars (label as proxy).
2. **Unusual volatility**: realized vol (last N bars) vs trailing baseline
   z-score; also true-range spike vs ATR.
3. **Unusual volume**: 1m/1d volume z-score vs same-time-of-day baseline.
4. All three → dq-style `anomalies` table + alert kinds (anomaly_imbalance,
   anomaly_vol, anomaly_volume) respecting the existing per-user alert engine +
   thresholds env-tunable; /api/anomalies + an "UNUSUAL ACTIVITY" panel (home +
   symbol page). Honest labeling: z-scores + baselines shown, proxies flagged.

## Build stages (launch AFTER the all-phases workflow lands; sequential)
1. **Filings intelligence**: internal/ingest/edgar extensions — filings feed
   (submissions API w/ form-type→plain-English map), Form 4 insider parse,
   13F holdings parse, dilution flags. Tables: filings, insider_trades,
   inst_holdings, dilution_flags. Workers rate-limited (SEC UA, ≤10 req/s,
   universe-scoped, slow cadence). APIs + web pages: /filings, /insiders,
   /institutions (+ per-company sections on symbol page).
2. **Congressional trades**: ingest worker (free mirrors, graceful absence),
   table + /congress page. Label data-lag honestly (disclosures lag 30-45d).
3. **Anomaly layer** (imbalance/vol/volume) as above.
4. **Signal8-style home**: ticker tape (indices/sectors/BTC/VIX), Top Stories +
   filings-mixed news feed, movers w/ mcap filter, calendars (econ from FRED;
   earnings-if-available), all in existing dark terminal aesthetic.
5. Review (rate-limit safety, EDGAR parse correctness on fixture filings,
   anomaly math, honest labels) → deploy → commit.

Honesty notes: filings/insider/13F/congress data are public-domain government
data — fully redistributable (unlike vendor OHLCV). Anomaly z-scores are
descriptive statistics, not predictions; label baselines. Congressional data
lags by law; show the lag.
