# SignalDeck — The Flywheel Plan (2026-07-03)

Goal: the best honest stock-prediction + data-storing system a single machine can run.
The unifying idea: **every subsystem produces data the others learn from, and nothing
is ever thrown away — it compounds.**

## The core insight (why this plan)

SignalDeck already *measures* itself (scores→outcomes, predictions→calibration). What it
doesn't yet do is **feed those measurements back in**. Today the ensemble weights are
static, sentiment is displayed but never used, ranking/regime/breakouts are separate tabs
instead of features, and pruned bars are deleted (data shrinks). Fix those four things and
the system becomes a flywheel: more data → better features → measured predictions →
outcome attribution → smarter weights → better predictions — automatically, forever.

## 1. How data is stored (permanence + provenance)

- **Feature store** (`features` table: symbol_id, horizon, ts, version, JSON vector):
  the EXACT feature vector used for every prediction is persisted at prediction time.
  This turns operations into an ever-growing labeled training set (vector + realized
  outcome) — reproducible retraining, no lookahead, no recompute drift.
- **Compaction, not deletion**: minute bars past retention roll up into hourly bars
  *before* pruning; daily bars and hourly rollups are kept forever. News, insights,
  predictions, outcomes, regime changes: never pruned. Disk grows slowly (compact forms),
  information never lost.
- **Dataset accounting**: `/api/datastats` — rows/bytes per table, oldest/newest ts,
  growth rate; rendered on /quality so growth is visible and provable.

## 2. How it continuously grows

- **Outcome accretion**: every score/prediction still seeds its own future label (exists).
- **Universe auto-discovery**: a worker finds candidate symbols (top movers/dollar volume
  via Alpaca) and surfaces them on /screener as one-click adds; auto-adds top candidates
  while under a symbol budget (default cap 30, env-tunable). More symbols → more
  cross-sectional data → better ranking/correlation/regime stats.
- **Sentiment archive → features**: daily per-symbol sentiment aggregates stored
  permanently, becoming a time series feature (not just a news feed).
- **Nightly learning pass**: adaptive-weights worker recomputes component attribution
  from all resolved outcomes (see §3) — the model literally gets smarter as data accrues.

## 3. How the systems/tabs help each other grow (the flywheel)

- **Regime → Ensemble**: component weights are learned *per regime* (what predicts in a
  squeeze differs from a downtrend).
- **Sentiment → Ensemble**: daily sentiment aggregate joins score/expectancy/forecast as a
  4th component — but only weighted once its measured IC clears the honesty gate.
- **Ranking → Ensemble**: cross-sectional percentile becomes a component input too.
- **Outcomes → Weights**: per-component attribution (hit-rate/IC per regime, from resolved
  prediction_outcomes) drives the weights, persisted + versioned; static prior until n≥30
  per cell (honesty gate — never pretend to have learned what isn't measured yet).
- **Breakouts + Regime-changes → Alerts**: events become actionable pings instead of a tab
  you must remember to open.
- **Everything → Daily Briefing**: one 7am ET synthesis (macro gate, watchlist movers,
  regime shifts, top-conviction calibrated predictions, portfolio grade) pinned on home.
- **Predict page shows its own wiring**: live component weights per regime, so the user
  sees WHAT the model currently believes about itself.

## 4. Build waves (autonomous)

- **Wave A** (storage + product): feature store + capture; compaction-not-deletion;
  /api/datastats + quality panel ‖ alerts engine (rules: breakout, regime change,
  prediction crossing, watchlist symbols only) + alerts table/API/web bell + macOS
  notify; daily-briefing worker + home pin.
- **Wave B** (learning + growth): sentiment daily aggregates + 4th ensemble component;
  per-regime adaptive weights from outcome attribution (gated, versioned, shown on
  /predict) ‖ universe auto-discovery worker + /screener discover panel + symbol budget.
- **Wave C**: tests for all new seams, adversarial review, full build, deploy via
  launchd kickstart, commit.

Honesty rules carried through every wave: no component gets weight without measured
evidence; gates and fallbacks explicit in UI; new claims ship with the stats that
prove them.
