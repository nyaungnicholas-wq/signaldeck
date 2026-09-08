# SignalDeck Product Specification

## Purpose and positioning

SignalDeck is a self-grading market-research instrument. A Go daemon records US stock and crypto market data, computes signals, freezes pre-registered forecasts into a hash-chained prediction ledger before outcomes exist, grades them against realized bars, and publishes verdicts including failures. The ambition is rigor and clarity, not a claim of institutional equivalence or a trading edge. It is not an auto-trader and does not provide advice.

## Primary journeys

### Journey 1: What is happening in markets, and what deserves my attention?

**Intended user:** Anyone scanning the public record or a signed-in workspace for a validated, regime-aware view of current conditions.  
**Starting question:** What is the market regime today, which symbols show pressure, and where is the evidence?  
**Entry page(s):** `/` (landing), `/market/overview`, `/dashboard` (session).  
**Required data:** Alpaca IEX daily bars (stocks), Kraken/TickStream (crypto), FRED VIX, daemon-computed pressure scores, regime classifications, publication status from `/api/accuracy`.  
**Useful output:** Ranked screener by pressure score, movers list, regime label, today's validated volatility-regime signal, top insight, market gauges, publication verdict or refusal reason.  
**Necessary explanation:** Regime definitions, pressure score composition, why publication may be refused (collapsed window), what "no qualified read today" means.  
**Failure and insufficient-evidence states:** Refusal notice with summarized reason and full grader text behind details; "no qualified read today" on dashboard; daemon unreachable state; licence refusal (HTTP 451) for raw data.  
**Completion criteria:** The page renders the verdict or a named withheld state, or a labelled loading state within 2 seconds and the result within 15 seconds on a quiet daemon; no number appears without its horizon, sample size, and as-of time.

### Journey 2: Help me understand this asset and the uncertainty around it

**Intended user:** A researcher or practitioner drilling into a single symbol.  
**Starting question:** What does the price structure show, which validated signals apply, what is the measured walk-forward accuracy per conviction band, and what usually happens next?  
**Entry page(s):** `/s/[market]/[symbol]` (symbol research page).  
**Required data:** Daily bars, indicators, patterns, structural signals with walk-forward accuracy per band, pressure decomposition, news, unusual activity, SEC EDGAR filings, FINRA short volume, insider/institution/congress trades, the symbol's own agent record, experimental directional P(up) marked experimental.  
**Useful output:** Price chart with indicators and patterns, validated signals with measured accuracy, "why it's moving" evidence, pressure decomposition, historical conditional outcomes, agent record, experimental forecast clearly labelled.  
**Necessary explanation:** Conviction bands and how accuracy is measured walk-forward; why experimental P(up) is withheld from the flagship record; data licensing limits (raw data never redistributed anonymously).  
**Failure and insufficient-evidence states:** "Insufficient" cards where evidence floors are not met; experimental label on directional probability; licence refusal (HTTP 451) for raw data download.  
**Completion criteria:** The page renders each section, or its named withheld state, with a labelled loading state within 2 seconds and the result within 15 seconds on a quiet daemon; every figure carries horizon, sample size, as-of time, and baseline.

### Journey 3: What does this forecast mean, and how trustworthy is it?

**Intended user:** Anyone reading a published forecast or the public accuracy record.  
**Starting question:** What was predicted, when was it frozen, how was it graded, and what is the verdict?  
**Entry page(s):** `/accuracy`, `/proof`, `/volatility`, `/dashboard` (forecast track record strip).  
**Required data:** Hash-chained prediction ledger (~477k entries), grader sentences, realized bars, design-effect-corrected intervals, distinct-day gates, QLIKE loss for volatility, 60-distinct-day floor.  
**Useful output:** Publication verdict per predictor with grader sentence quoted and labelled; honesty note when verdict is not resolved by intervals; dated historical retirement record (flagship directional model retired 2026-07-24 by pre-registered rule); hash-chain re-verification; live track record with day-count and deduplicated symbol-day gates; skill withheld until gates clear; volatility forecast vs random walk and RiskMetrics EWMA with QLIKE loss.  
**Necessary explanation:** Hash-chain mechanics; design effect and distinct-day floors; why skill is withheld; what "collapsed window" means (currently refusing 2026-07-17..08-06); why forecast coverage abstains by design (~6-10% of symbols).  
**Failure and insufficient-evidence states:** Refusal notice (summary + full grader text); "insufficient" cards on volatility (currently 1 of 60 days); withheld skill on track record; publication gate refuses figures over collapsed window.  
**Completion criteria:** Every verdict or refusal renders within 15 seconds on a quiet daemon (the API answers in under 100 ms once cached); each figure shows horizon, distinct-day count, data-as-of, and baseline.

### Journey 4: Has this strategy worked under defensible assumptions?

**Intended user:** A researcher evaluating a strategy in the lab.  
**Starting question:** What does a backtest show under next-bar fills with no lookahead, and what remains pending until live-graded?  
**Entry page(s):** `/lab/backtest`, `/lab/signal-backtest`, `/lab/track-record`, `/lab/honesty`.  
**Required data:** Historical bars, signal definitions, next-bar fill logic, cost model, live-graded outcomes for comparison.  
**Useful output:** Backtest results labelled PENDING until live-graded; signal backtest with walk-forward accuracy; track record with integrity boundary refusing to summarize performance across back-dated fills; honesty page documenting limitations.  
**Necessary explanation:** PENDING label meaning; integrity boundary on back-dated fills; why every measured leg that ranks backwards is dropped (not down-weighted); costs and assumptions in the cost model.  
**Failure and insufficient-evidence states:** PENDING label on all backtests until live-graded; integrity boundary refusal on back-dated fill summaries; "insufficient" where sample floors not met.  
**Completion criteria:** Backtest renders with the PENDING label and all assumptions exposed, with a labelled running state while it computes; no summary appears without live-grade comparison where available.

### Journey 5: What can I learn from a paper-trading decision?

**Intended user:** A practitioner simulating decisions with a manual market-order book.  
**Starting question:** If I had entered this position at the next bar with stated costs, what would the outcome be, and what does the simulation not capture?  
**Entry page(s):** `/lab/paper`, `/lab/portfolio`, `/lab/risk`.  
**Required data:** Live bars, manual order book, cost model, position tracking, risk metrics.  
**Useful output:** Paper trading simulation with manual market-order book and cost model; portfolio view; risk metrics; track record with integrity boundary.  
**Necessary explanation:** Simulation is not execution; cost model assumptions; integrity boundary refuses cross-fill summaries; no real-money execution inside the product.  
**Failure and insufficient-evidence states:** Integrity boundary refusal; daemon unreachable state; licence refusal for raw data.  
**Completion criteria:** Simulation renders with all costs and assumptions visible, and a refused order states its reason (no fresh quote, oversell); no performance figure appears without its horizon, sample, and as-of time.

## Default path and progressive disclosure

The public record comes first: landing, accuracy, proof, volatility, glossary, health. No account required. The workspace's simple view adds Home (dashboard), Market (overview, signals, breadth, regimes, macro), Watchlist, and an "Advanced" door. Behind the Advanced door sit INTEL (news, SEC filings, insiders, institutions, shorts, congress trades) and LAB (35 surfaces including backtest composer, signal backtest, paper trading, track record, honesty, forecasts/models, live pipeline health, portfolio, risk, optimizer, options, pairs, sentiment, scenario, strategies, system). INTEL and LAB are hidden by default because they are research tools with steep evidence contracts; the simple view serves the primary journeys without overwhelming the user.

## Evidence display contract

Every important result should expose:

| Field | Where satisfied | Where partial or missing |
|-------|-----------------|--------------------------|
| What was measured | `/accuracy` (grader sentence), `/proof` (hash-chain), `/volatility` (QLIKE), symbol page (walk-forward accuracy per band) | Lab backtests labelled PENDING — measurement not yet live-graded |
| Population and horizon | `/accuracy` (per predictor), `/proof` (distinct-day gates), `/volatility` (60-day floor), symbol page (conviction bands) | Lab surfaces: horizon shown but population sometimes implicit |
| Data-as-of | All public pages (daemon timestamp), dashboard (as-of badge) | Some lab surfaces rely on stale cache under worker load |
| Live/backtest/simulated/illustrative | `/accuracy` (live), `/proof` (live), `/volatility` (live), lab backtests (PENDING label), paper trading (simulation label) | Lab/forecasts: some model outputs not yet tagged |
| Baseline | `/volatility` (random walk, RiskMetrics EWMA), `/accuracy` (grader baseline), symbol page (conditional outcomes) | Lab/optimizer, lab/pairs: baseline not consistently exposed |
| Sample size and distinct periods | `/proof` (day-count, deduplicated symbol-day gates), `/volatility` (60 distinct days), `/accuracy` (distinct-day floors) | Lab backtests: sample shown but distinct-period correction not always applied |
| Uncertainty | `/accuracy` (design-effect-corrected intervals), `/proof` (skill withheld until gates clear), `/volatility` (QLIKE distribution) | Symbol page: experimental P(up) shows no interval; lab surfaces often omit |
| Costs and assumptions | `/lab/paper` (cost model), `/lab/backtest` (next-bar fills, no lookahead), `/lab/honesty` (limitations) | Lab/optimizer, lab/options: cost assumptions not always surfaced |
| Limitations | `/accuracy` (honesty note when verdict unresolved), `/proof` (withheld skill), `/volatility` (insufficient cards), `/lab/honesty` (dedicated page) | Many lab surfaces lack explicit limitation callouts |
| Source and method | `/glossary` (terms), `/proof` (hash-chain method), daemon endpoints (data sources listed) | Lab/debate, lab/desk (AI): method not documented |

## Design system

**Tokens (CSS variables):** `--bg`, `--panel`, `--text`, `--dim`, `--faint`, `--accent` (amber), `--ok`, `--warn`, `--bad`.  
**Typography:** Inter for UI, JetBrains Mono for numbers (`.tnum` class). Type floor 12px.  
**Panels:** `.panel` with header strip `.panel-h`.  
**Shared state components:** Skeleton (loading), EmptyState, ErrorState, FreshnessBadge (stale), RefusalNotice (summarized reason + full text behind `<details>`), VerdictCard (success/verdict), PagePurpose (one line per page), StorySection (numbered narrative sections). Chips for tags. Nav-link states.  
**Rules:** Numbers in mono with tabular figures; never below 12px; one page-purpose line per page; refusal never dumps raw grader text (summary first, full text behind details); tables scroll inside `.table-wrap` within their panel; the page never scrolls sideways.

## Feature inventory and decisions

| Surface | Decision | Reason |
|---------|----------|--------|
| landing | Keep (default path) | Public entry; shows publication status or refusal reason |
| accuracy | Keep (default path) | Core public verdict record; grader sentences quoted |
| proof | Keep (default path) | Hash-chain verification; live track record with gates |
| volatility | Keep (default path) | HAR vs baselines; QLIKE; 60-day floor; honest insufficient state |
| glossary | Keep (default path) | Plain-English terms for evidence contract |
| dashboard | Keep (default path) — Repair needed | Today's validated read, regime signal, track-record strip; three overlapping onboarding surfaces (setup checklist, goal banner, "new here" popover) and eight header chips to consolidate |
| market overview | Keep (default path) | Screener, movers, regime — primary scanning surface |
| symbol page | Keep (default path) — Repair needed | Deep research; complete but ~6,000 px long; collapse secondary panels by default |
| watchlist | Keep (default path) | Personal tracking; compare view |
| intel/news | Keep (advanced) | Behind Advanced door; licensed sources |
| intel/congress | Keep (advanced) | Behind Advanced door; niche use case |
| lab/backtest | Keep (advanced) | Composer with next-bar fills; PENDING label enforced |
| lab/paper | Keep (advanced) | Manual order book; cost model; integrity boundary |
| lab/track-record | Keep (advanced) | Live-graded only; integrity boundary on back-dated fills |
| lab/honesty | Keep (advanced) | Documents limitations; required by evidence contract |
| lab/forecasts | Keep (advanced) | Model registry; live pipeline health |
| lab/live | Keep (advanced) | Pipeline health; daemon status |
| lab/optimizer | Research-only | Not on default path; assumptions need surfacing |
| lab/options | Research-only | Not on default path; experimental |
| lab/pairs | Research-only | Not on default path; experimental |
| lab/debate (AI) | Narrow | AI surface; narrow to specific research question |
| lab/desk (AI) | Narrow | AI surface; narrow to specific research question |
| hud | Research-only | Personal PUSH-20 sync, deliberately outside the product nav |

## Non-goals

Auto-trading, advice, profitability claims, real-money execution inside the product.