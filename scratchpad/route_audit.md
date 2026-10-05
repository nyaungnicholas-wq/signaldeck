# Route audit — Stage 1 (2026-07-04, live daemon :8322 / web :8323, logged OUT)

Verdicts: **OK** = real data renders · **EMPTY** = renders, no data, honest reason ·
**AUTH** = 401 until login (by design) · **PARTIAL** = some panels empty.
No page or API returned a hard 5xx/crash. All 28 built page routes return 200.

## Page routes (28/28 → HTTP 200, no `__next_error__`)

| Route | HTTP | Data verdict | Empty-reason / notes |
|---|---|---|---|
| `/` | 200 | OK | tape (10), movers, insights, news all live |
| `/hud` | 200 | OK | HUD available:true, live PUSH-20 equity/pnl |
| `/forecast` | 200 | OK | per-symbol logit forecasts w/ brier/auc |
| `/insights` | 200 | OK | 2318+ insights |
| `/honesty` | 200 | OK | buckets small-n, gate captions honest |
| `/screener` | 200 | OK | 511 symbols (1.6MB payload) |
| `/quality` | 200 | OK | dq events incl. worker_stale rows |
| `/agents` | 200 | PARTIAL | runs render, but rare-cadence workers (13f-poller, backup, fred-poller, universe-poller, congress-poller, tape-seeder, edgar-fetcher) invisible — worker_runs pruned to 2000 global rows while crypto-live flaps every ~5s (tickstream :8321 down). FIXED in source: per-worker keep-20 floor in PruneWorkerRuns. Needs daemon redeploy. |
| `/backtest` | 200 | OK | POST /api/backtest on demand |
| `/predict` | 200 | OK | predictions + calibration render (cal bins mostly n=0 — honest) |
| `/regime` | 200 | OK | states + changes |
| `/news` | 200 | OK | Alpaca news live |
| `/macro` | 200 | PARTIAL | breadth/vol OK; FRED series EMPTY (`/api/macro-series?series=VIXCLS` count 0) — FRED WAF killed the old anonymous UA (h2 INTERNAL_ERROR). FIXED in source (declarative UA); populates after redeploy + fred-poller run (6h cadence, also runs at boot). |
| `/risk` | 200 | OK | POST /api/risk on demand |
| `/portfolio` | 200 | AUTH | /api/portfolio 401 logged-out (per-user) |
| `/ai` | 200 | PARTIAL | /api/ai/status OK; /api/ai/analyst 502 "daily AI call limit reached — resets at UTC midnight" (honest spend cap, not a crash) |
| `/alerts` | 200 | AUTH | /api/alerts 401 logged-out (per-user) |
| `/login` | 200 | OK | — |
| `/track-record` | 200 | OK | small resolved-n, honest coverage |
| `/paper` | 200 | OK | sim book just started (equity=100k, 1 point) |
| `/filings` | 200 | EMPTY | count 0 — SEC EDGAR 403'd ALL requests (WAF rejected old UA). ROOT CAUSE FIXED (declarative UA verified live 200). Data arrives after daemon redeploy: filings-poller 2h cadence + at boot. |
| `/insiders` | 200 | EMPTY | insider_trades 0 rows — Form 4 fetch behind same EDGAR 403. Fixed as above; ~2-business-day legal lag note intact. |
| `/institutions` | 200 | EMPTY | inst_holdings 0 rows — 13F index/doc fetch 403 (10 dq `13f_parse_error` events). Fixed as above; curated 25-manager list renders. |
| `/congress` | 200 | EMPTY | both Stock Watcher S3 mirrors dead (bucket AccessDenied — NOT a UA issue). Honest lagNote + per-chamber outage status served; `SIGNALDECK_SENATE/HOUSE_TRADES_URL` override ready. Stays empty until a live mirror exists. |
| `/s/stocks/AAPL` | 200 | OK | bars(502d)/scores/overlays live; fundamentals section empty until EDGAR sweep (edgar-fetcher 24h + boot) |
| `/s/crypto/BTC%2FUSD` | 200 | OK | live snaps (bid/ask/imb) |
| `/trends` | 200 | OK | (extra route in build) |
| `/signal-backtest` | 200 | OK | gated result honest (independentN 1 < 30) |

## Key APIs (daemon :8322, same via :8323 proxy)

| Endpoint | Status | Data | Note |
|---|---|---|---|
| /api/health | 200 | OK | uptime, alpaca:true |
| /api/symbol, /api/bars, /api/scores/history, /api/chart-overlays | 200 | OK | AAPL: 502 daily bars, 4.3MB score history |
| /api/screener, /api/trends, /api/ranking, /api/sectors, /api/movers, /api/tape | 200 | OK | |
| /api/honesty, /api/quality, /api/agents, /api/insights, /api/hud | 200 | OK | |
| /api/predictions?symbol&market, /api/regime-conditioned?…, /api/ledger?…, /api/snaps?…&market | 200 | OK | 404 "need symbol= and market=" ONLY when params omitted (correct validation, not a bug) |
| /api/model-forecasts?symbol=AAPL | 200 | `null` | no GBM/meanrev legs stored yet for AAPL (honest no-edge) |
| /api/forecast, /api/calibration, /api/track-record, /api/paper, /api/signal-backtest | 200 | OK | small-n gates honest |
| /api/news, /api/breakouts, /api/candidates, /api/adaptive, /api/universe, /api/datastats, /api/calendar | 200 | OK | |
| /api/macro | 200 | PARTIAL | breadth OK, FRED-driven fields pending redeploy |
| /api/macro-series?series=VIXCLS | 200 | EMPTY | FRED UA fix pending redeploy |
| /api/filings, /api/insiders, /api/fundamentals, /api/dilution | 200 | EMPTY | EDGAR 403 root cause fixed; awaits redeploy + sweeps |
| /api/institutions | 200 | EMPTY(holdings) | curated list present |
| /api/congress | 200 | EMPTY | dead mirrors (see above) |
| /api/anomalies | 200 | EMPTY | no current anomalies (descriptive layer, honest) |
| /api/correlation | 200 | EMPTY | matrix [] — needs more overlapping history |
| /api/watchlist, /api/alerts, /api/portfolio, /api/auth/me | 401 | AUTH | expected logged-out |
| /api/ai/status | 200 | OK | charters render |
| /api/ai/analyst | 502 | CAP | honest daily-cap message; resets UTC midnight |

## Counts
- Pages: 28 audited → 28 HTTP 200 · 0 broken/5xx · 19 OK · 3 EMPTY-EDGAR (fixed, pending redeploy) · 1 EMPTY-dead-mirror (congress) · 2 PARTIAL (macro/FRED fixed pending redeploy; ai spend cap) · 1 PARTIAL agents-visibility (fixed pending redeploy) · 2 AUTH-gated.
- APIs: 0 hard failures; all "404"s were missing-param validation; the single 502 is the honest LLM spend cap.

## Redeploy note for later stages
The RUNNING daemon still has the old binary. The UA + prune + finish-record
fixes are source-only until `daemon/signaldeckd` is rebuilt/restarted. After
restart, boot-time runs of edgar-fetcher/filings-poller/13f-poller/fred-poller
should populate filings/insiders/institutions/fundamentals/VIX within minutes.
Also: tickstream (:8321) is DOWN → crypto-live errors every ~5s (flooding
worker_runs and blocking fresh BTC snapshots' upstream); restart tickstream to
stop the flap.
