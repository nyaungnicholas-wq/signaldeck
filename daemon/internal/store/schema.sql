-- SignalDeck schema v1. Times are unix seconds UTC. Prices are REAL here
-- (analytics store, not an execution venue); exact-decimal work stays in
-- TickStream where checksums require it.

CREATE TABLE IF NOT EXISTS symbols (
  id       INTEGER PRIMARY KEY,
  symbol   TEXT NOT NULL,
  market   TEXT NOT NULL CHECK (market IN ('crypto','stocks')),
  name     TEXT NOT NULL DEFAULT '',
  active   INTEGER NOT NULL DEFAULT 1,
  added_at INTEGER NOT NULL,
  UNIQUE (symbol, market)
);

CREATE TABLE IF NOT EXISTS bars (
  symbol_id INTEGER NOT NULL,
  tf        TEXT NOT NULL CHECK (tf IN ('1m','1h','1d')),
  ts        INTEGER NOT NULL,
  open REAL NOT NULL, high REAL NOT NULL, low REAL NOT NULL, close REAL NOT NULL,
  volume REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (symbol_id, tf, ts)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS snapshots_1s (
  symbol_id    INTEGER NOT NULL,
  ts           INTEGER NOT NULL,
  bid REAL, ask REAL, mid REAL, wmid REAL,
  imb_signed REAL, spread REAL,
  apply_lat_ns INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (symbol_id, ts)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS scores (
  symbol_id  INTEGER NOT NULL,
  horizon    TEXT NOT NULL CHECK (horizon IN ('1h','1d','1w')),
  ts         INTEGER NOT NULL,
  score      REAL NOT NULL,
  components TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (symbol_id, horizon, ts)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_scores_ts ON scores (ts);

CREATE TABLE IF NOT EXISTS score_outcomes (
  symbol_id   INTEGER NOT NULL,
  horizon     TEXT NOT NULL,
  ts          INTEGER NOT NULL,
  score       REAL NOT NULL,
  fwd_return  REAL,
  resolved_at INTEGER,
  PRIMARY KEY (symbol_id, horizon, ts)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_outcomes_unresolved
  ON score_outcomes (resolved_at) WHERE resolved_at IS NULL;

CREATE TABLE IF NOT EXISTS expectancy (
  symbol_id  INTEGER NOT NULL,
  horizon    TEXT NOT NULL,
  state_key  TEXT NOT NULL,
  n          INTEGER NOT NULL,
  mean_fwd   REAL NOT NULL,
  median_fwd REAL NOT NULL,
  hit_rate   REAL NOT NULL,
  stdev      REAL NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, horizon, state_key)
);

CREATE TABLE IF NOT EXISTS insights (
  id        INTEGER PRIMARY KEY,
  scope     TEXT NOT NULL CHECK (scope IN ('symbol','market')),
  symbol_id INTEGER,
  ts        INTEGER NOT NULL,
  headline  TEXT NOT NULL,
  body      TEXT NOT NULL,
  data      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_insights_ts ON insights (ts DESC);

CREATE TABLE IF NOT EXISTS worker_runs (
  id          INTEGER PRIMARY KEY,
  worker      TEXT NOT NULL,
  started_at  INTEGER NOT NULL,
  finished_at INTEGER,
  status      TEXT NOT NULL DEFAULT 'running',
  detail      TEXT NOT NULL DEFAULT '',
  revision    TEXT NOT NULL DEFAULT ''  -- writing binary's lineage.RevisionStamp ('' = unknown)
);
CREATE INDEX IF NOT EXISTS idx_worker_runs ON worker_runs (worker, started_at DESC);

CREATE TABLE IF NOT EXISTS dq_events (
  id        INTEGER PRIMARY KEY,
  symbol_id INTEGER,
  ts        INTEGER NOT NULL,
  kind      TEXT NOT NULL,
  detail    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_dq_ts ON dq_events (ts DESC);

CREATE TABLE IF NOT EXISTS hud_summary (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  fetched_at INTEGER NOT NULL,
  payload    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);

-- Latest backtested directional forecast per symbol+horizon. A forecast is
-- ONLY shown with its out-of-sample grade (lift <= 0 means "no edge").
CREATE TABLE IF NOT EXISTS forecasts (
  symbol_id INTEGER NOT NULL,
  horizon   TEXT NOT NULL,
  ts        INTEGER NOT NULL,
  prob      REAL NOT NULL,
  accuracy  REAL NOT NULL,
  brier     REAL NOT NULL,
  auc       REAL NOT NULL,
  base_rate REAL NOT NULL,
  lift      REAL NOT NULL,
  n_train   INTEGER NOT NULL,
  n_eval    INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, horizon)
);

-- Paper positions: logged discretionary "reads" the portfolio page grades.
CREATE TABLE IF NOT EXISTS positions (
  id             INTEGER PRIMARY KEY,
  symbol_id      INTEGER NOT NULL,
  qty            REAL NOT NULL,
  entry_price    REAL NOT NULL,
  entry_ts       INTEGER NOT NULL,
  note           TEXT NOT NULL DEFAULT '',
  score_at_entry REAL NOT NULL DEFAULT 0,
  open           INTEGER NOT NULL DEFAULT 1,
  exit_price     REAL,
  exit_ts        INTEGER
);
CREATE INDEX IF NOT EXISTS idx_positions_open ON positions (open);

-- Calibrated ensemble prediction (fuses score+expectancy+forecast) + its
-- outcome, so the reliability/calibration curve is fed at write time.
CREATE TABLE IF NOT EXISTS predictions (
  symbol_id  INTEGER NOT NULL,
  horizon    TEXT NOT NULL,
  ts         INTEGER NOT NULL,
  raw_prob   REAL NOT NULL,
  cal_prob   REAL NOT NULL,
  n_used     INTEGER NOT NULL,
  components TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (symbol_id, horizon, ts)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS prediction_outcomes (
  symbol_id   INTEGER NOT NULL,
  horizon     TEXT NOT NULL,
  ts          INTEGER NOT NULL,
  prob        REAL NOT NULL,   -- calibrated prob at prediction time
  up          INTEGER,         -- realized 1/0, NULL until resolved
  fwd_return  REAL,
  resolved_at INTEGER,
  PRIMARY KEY (symbol_id, horizon, ts)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_predoutcomes_unresolved
  ON prediction_outcomes (resolved_at) WHERE resolved_at IS NULL;

-- Latest regime per symbol + a log of regime CHANGES (the transition signal).
CREATE TABLE IF NOT EXISTS regime_state (
  symbol_id INTEGER PRIMARY KEY,
  ts        INTEGER NOT NULL,
  label     TEXT NOT NULL,
  strength  REAL NOT NULL,
  note      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS regime_changes (
  id        INTEGER PRIMARY KEY,
  symbol_id INTEGER NOT NULL,
  ts        INTEGER NOT NULL,
  from_lbl  TEXT NOT NULL,
  to_lbl    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_regime_changes ON regime_changes (ts DESC);

-- Daily cross-sectional relative-strength ranking snapshot.
CREATE TABLE IF NOT EXISTS rankings (
  ts        INTEGER NOT NULL,
  symbol_id INTEGER NOT NULL,
  score     REAL NOT NULL,   -- 0..100 percentile
  rank      INTEGER NOT NULL,
  ret1m     REAL NOT NULL DEFAULT 0,
  ret3m     REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (ts, symbol_id)
) WITHOUT ROWID;

-- Detected breakout / squeeze / correlation-break events.
CREATE TABLE IF NOT EXISTS breakouts (
  id        INTEGER PRIMARY KEY,
  symbol_id INTEGER,
  ts        INTEGER NOT NULL,
  kind      TEXT NOT NULL,
  detail    TEXT NOT NULL DEFAULT '',
  strength  REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_breakouts_ts ON breakouts (ts DESC);

-- News headlines (from Alpaca) + LLM-tagged sentiment.
CREATE TABLE IF NOT EXISTS news (
  id         TEXT PRIMARY KEY,       -- provider article id (dedup)
  symbol_id  INTEGER NOT NULL,
  ts         INTEGER NOT NULL,       -- article time (unix s)
  headline   TEXT NOT NULL,
  url        TEXT NOT NULL DEFAULT '',
  source     TEXT NOT NULL DEFAULT '',
  sentiment  TEXT NOT NULL DEFAULT 'unrated',  -- bullish|bearish|neutral|unrated
  score      REAL NOT NULL DEFAULT 0,          -- -1..+1
  rationale  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_news_symbol_ts ON news (symbol_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_news_unrated ON news (sentiment) WHERE sentiment='unrated';

-- Multi-user: accounts, browser sessions, per-user watchlists.
CREATE TABLE IF NOT EXISTS users (
  id         INTEGER PRIMARY KEY,
  username   TEXT UNIQUE NOT NULL,
  pass_hash  TEXT NOT NULL,
  created_ts INTEGER,
  is_admin   INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id),
  created_ts INTEGER,
  expires_ts INTEGER
);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions (expires_ts);

CREATE TABLE IF NOT EXISTS user_symbols (
  user_id   INTEGER NOT NULL REFERENCES users(id),
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  added_ts  INTEGER,
  PRIMARY KEY (user_id, symbol_id)
);

-- ── storage-permanence wave (appended block — keep at END of file so ──────
-- ── parallel schema edits by other agents never collide) ─────────────────

-- Feature store: the EXACT input vector the ensemble used for a prediction,
-- persisted at prediction time. Joined to prediction_outcomes it becomes an
-- ever-growing labeled training set (no lookahead, no recompute drift).
-- NEVER pruned.
CREATE TABLE IF NOT EXISTS features (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  horizon   TEXT NOT NULL,
  ts        INTEGER NOT NULL,
  version   INTEGER NOT NULL DEFAULT 1,
  vec       TEXT NOT NULL,           -- JSON object of name -> float64
  UNIQUE (symbol_id, horizon, ts, version)
);
CREATE INDEX IF NOT EXISTS idx_features_sym_h_ts ON features (symbol_id, horizon, ts);

-- ─────────────────────────────────────────────────────────────────────────
-- ALERTS WAVE (appended block — do not merge into the sections above).
-- Per-user actionable alerts derived from breakout events, regime changes,
-- and calibrated predictions crossing conviction thresholds. seen=0 rows
-- drive the web bell badge; POST /api/alerts/seen flips them.
CREATE TABLE IF NOT EXISTS alerts (
  id        INTEGER PRIMARY KEY,
  user_id   INTEGER NOT NULL,
  symbol_id INTEGER,
  horizon   TEXT,
  kind      TEXT NOT NULL,  -- breakout | regime_change | prediction_high | prediction_low
  detail    TEXT NOT NULL DEFAULT '',
  ts        INTEGER NOT NULL,
  seen      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_alerts_user ON alerts (user_id, seen, ts DESC);
-- Idempotency: a partially-failed sweep re-reads events from the unadvanced
-- cursor, so re-inserting the same event must be a no-op (INSERT OR IGNORE
-- against this key). COALESCE because symbol_id/horizon are nullable and
-- SQLite treats NULLs as distinct in unique indexes.
-- detail is part of the key: two breakout KINDS on the same bar (donchian +
-- volume_spike) share (user,kind,ts,symbol) and differ only in detail.
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_dedup
  ON alerts (user_id, kind, ts, COALESCE(symbol_id, 0), COALESCE(horizon, ''), detail);

-- ─────────────────────────────────────────────────────────────────────────
-- LEARNING-FLYWHEEL WAVE (appended block — do not merge into sections above).
-- Daily per-symbol sentiment aggregates: rated news rows rolled up into a
-- permanent time series (day = YYYY-MM-DD UTC of the article timestamp).
-- This is the sentiment FEATURE the ensemble consumes — never pruned, so the
-- archive only grows. Recomputed idempotently by the sentiment-aggregator.
CREATE TABLE IF NOT EXISTS sentiment_daily (
  symbol_id  INTEGER NOT NULL,
  day        TEXT NOT NULL,               -- YYYY-MM-DD (UTC)
  n          INTEGER NOT NULL,            -- rated headlines that day
  mean_score REAL NOT NULL,               -- mean sentiment score, [-1,+1]
  pos        INTEGER NOT NULL DEFAULT 0,  -- bullish count
  neg        INTEGER NOT NULL DEFAULT 0,  -- bearish count
  neu        INTEGER NOT NULL DEFAULT 0,  -- neutral count
  PRIMARY KEY (symbol_id, day)
) WITHOUT ROWID;

-- ─────────────────────────────────────────────────────────────────────────
-- UNIVERSE-DISCOVERY WAVE (appended block — do not merge into sections above).
-- Candidate symbols surfaced by the universe-discovery worker (Alpaca
-- most-actives / movers screeners). status: new (awaiting review/auto-add),
-- added (promoted into `symbols`), dismissed (operator said no).
CREATE TABLE IF NOT EXISTS candidates (
  symbol        TEXT NOT NULL,
  market        TEXT NOT NULL DEFAULT 'stocks',
  first_seen_ts INTEGER NOT NULL,
  last_seen_ts  INTEGER NOT NULL,
  seen_count    INTEGER NOT NULL DEFAULT 1,
  dollar_vol    REAL NOT NULL DEFAULT 0,
  pct_change    REAL NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new','added','dismissed')),
  PRIMARY KEY (symbol, market)
);
CREATE INDEX IF NOT EXISTS idx_candidates_status ON candidates (status, dollar_vol DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- PER-SYMBOL AGENTS WAVE (appended block — do not merge into sections above).
-- Each symbol behaves differently, so each gets its OWN learned model: blend
-- weights, calibration map, per-component skill, and a plain-English
-- "personality", all derived ONLY from that symbol's own resolved outcomes
-- (prequential, no leakage — see internal/symbolagent). ONE worker computes
-- every symbol's row on a cheap upsert; nothing here is per-process/goroutine.
--
-- HONESTY: tier records which evidence tier actually produced the active model:
--   personal  — the symbol has >= MinPersonal of ITS OWN resolved outcomes for
--               this horizon, so its own weights + calibration are trusted;
--   regime    — too few personal samples; falls back to the global per-regime
--               learned weights (adaptive_weights:v1);
--   global    — falls back to the pooled "all" learned weights;
--   static    — no learned evidence anywhere yet: equal-weight prior.
-- A row is NEVER pruned (permanent self-knowledge record).
CREATE TABLE IF NOT EXISTS symbol_models (
  symbol_id   INTEGER NOT NULL REFERENCES symbols(id),
  horizon     TEXT    NOT NULL,
  weights     TEXT    NOT NULL DEFAULT '{}', -- JSON: component -> weight
  calibration TEXT    NOT NULL DEFAULT '{}', -- JSON: isotonic knots {kx:[],ky:[],fitted:bool}
  skill       TEXT    NOT NULL DEFAULT '{}', -- JSON: component -> {hitRate, ic, n}
  personality TEXT    NOT NULL DEFAULT '',   -- deterministic plain-English read of the skill
  n_samples   INTEGER NOT NULL DEFAULT 0,    -- this symbol+horizon's own resolved outcomes
  tier        TEXT    NOT NULL DEFAULT 'static' CHECK (tier IN ('personal','regime','global','static')),
  updated_ts  INTEGER NOT NULL DEFAULT 0,
  UNIQUE (symbol_id, horizon)
);
CREATE INDEX IF NOT EXISTS idx_symbol_models_sym ON symbol_models (symbol_id, horizon);

-- ─────────────────────────────────────────────────────────────────────────
-- FREE-DATA WAVE (Stage 2) (appended block — do not merge into sections above).
-- Two zero-cost, no-vendor macro/fundamental sources so the platform has real
-- cross-asset and company context WITHOUT a paid feed. Both degrade gracefully
-- when the upstream is unavailable; neither ever exposes redistribution-limited
-- Alpaca market data.
--
-- (1) FRED macro series (St. Louis Fed, keyless CSV endpoint). A long, thin
-- time series keyed by (series, ts): VIXCLS (VIX close), DGS10 (10y yield),
-- T10Y2Y (10y-2y spread, recession proxy), DFF (effective fed funds). ts is a
-- UTC-midnight day epoch (FRED observations are daily). value is the observed
-- level; missing observations (FRED emits "." on holidays) are skipped, so a
-- present row always carries a real number. Never pruned - macro history is
-- tiny and permanently useful as a Stage-6 cross-asset feature.
CREATE TABLE IF NOT EXISTS macro_series (
  series TEXT    NOT NULL,   -- FRED series id, e.g. VIXCLS / DGS10 / T10Y2Y / DFF
  ts     INTEGER NOT NULL,   -- observation day, UTC-midnight epoch seconds
  value  REAL    NOT NULL,   -- observed level
  PRIMARY KEY (series, ts)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_macro_series_ts ON macro_series (series, ts DESC);

-- (2) SEC EDGAR company fundamentals (free, no key; SEC requires a descriptive
-- User-Agent and <=10 req/s). Per universe stock, a handful of company-facts
-- (Revenues, EPS, shares outstanding) plus the latest filing date, each stamped
-- with the fact's as_of (the period end the value reports) and fetched_at (when
-- we pulled it). Keyed by (symbol_id, metric, as_of) so re-fetches are
-- idempotent and a metric's history accumulates. Slowly refreshed (~24h).
CREATE TABLE IF NOT EXISTS fundamentals (
  symbol_id  INTEGER NOT NULL REFERENCES symbols(id),
  metric     TEXT    NOT NULL,   -- Revenues | EPS | SharesOutstanding | LatestFilingDate | CIK
  value      REAL    NOT NULL,   -- numeric fact value (dates stored as epoch seconds)
  as_of      INTEGER NOT NULL,   -- period-end / effective date, epoch seconds
  fetched_at INTEGER NOT NULL,   -- when this row was fetched, epoch seconds
  PRIMARY KEY (symbol_id, metric, as_of)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_fundamentals_sym ON fundamentals (symbol_id, metric, as_of DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- PREDICTION LEDGER (Stage 3) (appended block — do not merge into the sections
-- above). An APPEND-ONLY, HASH-CHAINED audit log of every prediction the
-- flagship PredictionRunner emits — the tamper-evidence buyers/allocators
-- require. Each row commits the prediction's exact identity (symbol, horizon,
-- bar, probabilities, feature-vector hash, model version) at the instant it was
-- made, BEFORE any outcome can exist, and is cryptographically chained to the
-- previous row: entry_hash = sha256(prev_hash ‖ canonical-json(entry fields)).
-- Recomputing the chain reproduces every head; any silent UPDATE/DELETE of a
-- historical row breaks it and is detected at the exact seq. This table is
-- WRITE-ONCE per row: never UPDATE, never REPLACE, never DELETE — append only.
-- It is deliberately a SEPARATE record from predictions/prediction_outcomes:
-- those are the mutable working state (outcomes get resolved later); this is the
-- immutable proof of WHAT WAS CLAIMED, WHEN — the point being that it cannot be
-- back-dated after the outcome is known.
CREATE TABLE IF NOT EXISTS prediction_ledger (
  seq           INTEGER PRIMARY KEY AUTOINCREMENT, -- monotonic append order (chain index)
  predicted_at  INTEGER NOT NULL,   -- wall-clock unix seconds when the entry was appended
  symbol_id     INTEGER,            -- the predicted symbol (NULL-safe, but always set in practice)
  horizon       TEXT,               -- 1d | 1w
  bar_ts        INTEGER,            -- the prediction's bar timestamp (predictions.ts)
  raw_prob      REAL,               -- uncalibrated ensemble probability
  cal_prob      REAL,               -- calibrated probability (== prediction_outcomes.prob seed)
  feature_hash  TEXT,               -- sha256 of the persisted feature-vector JSON
  model_version INTEGER,            -- ledgerModelVersion const at emit time
  prev_hash     TEXT,               -- entry_hash of seq-1 ("" for the genesis row)
  entry_hash    TEXT    NOT NULL,   -- sha256(prev_hash ‖ canonical-json of this entry) — the chain link
  revision      TEXT                -- writing binary's lineage.RevisionStamp (see regime_outcomes.revision).
                                    -- NOT part of the chained payload: entry_hash is frozen by its 2026 definition
                                    -- and re-including a new field would break every prior link. The stamp is
                                    -- provenance, not a claim, so it rides beside the chain rather than inside it.
);
CREATE INDEX IF NOT EXISTS idx_prediction_ledger_sym
  ON prediction_ledger (symbol_id, horizon, seq DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- PAPER TRADING (Stage 4) (appended block — do not merge into the sections
-- above). An INTERNAL, SELF-CONTAINED, SIMULATED paper-trading book driven by
-- the platform's OWN live calibrated predictions. This is NOT connected to any
-- broker: no order is ever sent anywhere; every fill is computed from the
-- daemon's own stored bar data. It exists to accumulate an HONEST, out-of-sample
-- track record of "if you had traded the flagship signal, next-bar-open, with
-- realistic costs, what would the P&L have been?".
--
-- A "strategy" here is a simulated portfolio identity (e.g. "flagship-1d") that
-- starts flat with a notional book and trades one symbol-target at a time per
-- symbol. The RULE: when a symbol's latest CALIBRATED prediction crosses the
-- long threshold we target a long; when it crosses the flat threshold we exit;
-- entries/exits fill at the NEXT bar's OPEN after the prediction (never same-bar
-- — no lookahead) and pay a realistic per-side cost. These tables are the track
-- record and are NEVER pruned.

-- paper_positions: the CURRENT open position per (strategy, symbol). qty is in
-- shares/units (fractional allowed); avg_px is the cost basis; opened_ts is when
-- the position was entered. A flat symbol has NO row (positions are deleted on
-- exit), so the presence of a row means "currently long".
CREATE TABLE IF NOT EXISTS paper_positions (
  strategy  TEXT    NOT NULL,   -- simulated portfolio id, e.g. "flagship-1d"
  symbol_id INTEGER NOT NULL,
  qty       REAL    NOT NULL,   -- units held (>0 = long; we are long/flat only)
  avg_px    REAL    NOT NULL,   -- average entry price (cost basis)
  opened_ts INTEGER NOT NULL,   -- bar ts (open time) at which the position was entered
  PRIMARY KEY (strategy, symbol_id)
) WITHOUT ROWID;

-- paper_trades: the append-only trade log. One row per simulated fill (a buy to
-- open or a sell to close). px is the fill price (the next bar's OPEN); cost is
-- the dollar cost charged on THIS fill (per-side spread+slippage proxy); reason
-- records why the fill happened (the crossing + the prediction that drove it).
-- Never pruned — it is the audit trail behind the equity curve.
CREATE TABLE IF NOT EXISTS paper_trades (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  strategy  TEXT    NOT NULL,
  symbol_id INTEGER NOT NULL,
  side      TEXT    NOT NULL,   -- "buy" (open long) | "sell" (close long)
  qty       REAL    NOT NULL,   -- units transacted
  px        REAL    NOT NULL,   -- fill price = next bar OPEN after the signal
  cost      REAL    NOT NULL,   -- dollar cost charged on this fill (>=0)
  ts        INTEGER NOT NULL,   -- fill bar ts (open time), unix seconds
  reason    TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_paper_trades_strat
  ON paper_trades (strategy, ts);

-- paper_equity: the equity curve, one row per (strategy, ts) mark. cash is
-- uninvested notional; positions_value is the marked-to-market value of open
-- positions at that ts; equity = cash + positions_value. Marked forward as new
-- bars arrive; never pruned (the curve IS the track record).
CREATE TABLE IF NOT EXISTS paper_equity (
  strategy        TEXT    NOT NULL,
  ts              INTEGER NOT NULL,   -- mark time (bar open ts), unix seconds
  cash            REAL    NOT NULL,
  positions_value REAL    NOT NULL,
  equity          REAL    NOT NULL,
  PRIMARY KEY (strategy, ts)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_paper_equity_strat
  ON paper_equity (strategy, ts);

-- paper_cursor: per-strategy idempotency + book state. last_bar_ts is the newest
-- bar ts the paper-trader has already ACTED on for this strategy; a re-run on a
-- bar at/behind it is a no-op (no double-trade). cash carries the simulated
-- book's uninvested notional between runs; started_ts is when the book was
-- initialized (first run). Book notional at t0 = STARTING_CASH const.
CREATE TABLE IF NOT EXISTS paper_cursor (
  strategy    TEXT    NOT NULL PRIMARY KEY,
  last_bar_ts INTEGER NOT NULL DEFAULT 0,
  cash        REAL    NOT NULL,
  started_ts  INTEGER NOT NULL
) WITHOUT ROWID;

-- paper_epochs: the STRATEGY-CHANGE history of a simulated book.
--
-- A track record is a record OF something. When the rules change, the numbers
-- before and after describe two different strategies, and averaging across the
-- change reports a strategy that was never run. This table is the boundary that
-- makes that impossible to do by accident: an epoch runs from from_ts until the
-- next epoch's from_ts, and every published statistic is computed WITHIN one.
--
-- The book itself is CONTINUOUS across an epoch boundary — same cash, same open
-- positions. Only the measurement is split. That is why the boundary lives here
-- rather than in a new strategy id: the capital did not reset, so the ledger
-- must not pretend it did.
--
-- Append-only in spirit: epochs are upserted from a schedule declared in code
-- (pipeline.paperEpochSchedule) so a rebuilt database reconstructs the same
-- boundaries, and a past epoch's from_ts is history that must not move.
CREATE TABLE IF NOT EXISTS paper_epochs (
  strategy TEXT    NOT NULL,
  epoch    INTEGER NOT NULL,   -- 1-based, ascending in time
  from_ts  INTEGER NOT NULL,   -- inclusive; epoch 1 uses 0 = "since inception"
  label    TEXT    NOT NULL,   -- short name, e.g. 'triple-barrier'
  reason   TEXT    NOT NULL,   -- what changed and why the record splits here
  PRIMARY KEY (strategy, epoch)
) WITHOUT ROWID;

-- ─────────────────────────────────────────────────────────────────────────
-- STAGE 6 — GATED MODEL FORECAST LEGS (append-only block).
-- model_forecasts stores each NAMED model leg (currently 'gbm' and 'meanrev')
-- per symbol+horizon with its LATEST out-of-sample grade, exactly parallel to
-- the `forecasts` table (which holds only the linear logit). The ensemble reads
-- prob + lift and includes the leg ONLY when lift > 0 — the identical honesty
-- gate the logit forecast passes. One row per (symbol, horizon, model); the
-- trainer upserts it in place.
CREATE TABLE IF NOT EXISTS model_forecasts (
  symbol_id INTEGER NOT NULL,
  horizon   TEXT    NOT NULL,
  model     TEXT    NOT NULL,        -- 'gbm' | 'meanrev'
  ts        INTEGER NOT NULL,        -- when trained/graded, unix seconds
  prob      REAL    NOT NULL,        -- latest P(up) for the most recent bar
  accuracy  REAL    NOT NULL,
  brier     REAL    NOT NULL,
  auc       REAL    NOT NULL,
  base_rate REAL    NOT NULL,
  lift      REAL    NOT NULL,        -- OOS lift; leg is used by ensemble iff >0
  n_train   INTEGER NOT NULL,
  n_eval    INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, horizon, model)
) WITHOUT ROWID;

-- ─────────────────────────────────────────────────────────────────────────
-- SIGNAL8 WAVE — STAGE 1: SEC FILINGS INTELLIGENCE (appended block — do not
-- merge into the sections above). All four tables hold PUBLIC-DOMAIN US
-- government data (SEC EDGAR), which is free to store and redistribute.
-- Honesty: the data LAGS by law/process — Form 4 ~2 business days after the
-- trade, 13F quarterly with up to a 45-day lag — and the API notes say so.

-- filings: the per-symbol SEC filings feed pulled from the EDGAR submissions
-- API. id is the accession number (globally unique at the SEC), so INSERT OR
-- IGNORE makes every re-sweep idempotent. label is the plain-English reading
-- of the form type ("8-K — earnings release (Item 2.02)"), derived by
-- edgar.FormLabel at ingest time.
CREATE TABLE IF NOT EXISTS filings (
  id        TEXT PRIMARY KEY,          -- SEC accession number
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  form      TEXT NOT NULL,             -- raw form type ("4", "8-K", "S-3", …)
  filed_ts  INTEGER NOT NULL,          -- acceptance/filing time, unix seconds
  title     TEXT NOT NULL DEFAULT '',  -- primary-document description
  url       TEXT NOT NULL DEFAULT '',  -- EDGAR archive link to the document
  label     TEXT NOT NULL DEFAULT ''   -- plain-English form label
);
CREATE INDEX IF NOT EXISTS idx_filings_sym_ts ON filings (symbol_id, filed_ts DESC);
CREATE INDEX IF NOT EXISTS idx_filings_ts ON filings (filed_ts DESC);
CREATE INDEX IF NOT EXISTS idx_filings_form ON filings (form, filed_ts DESC);

-- insider_trades: parsed Form 4 (ownershipDocument XML). ONE row per filing
-- (accession PK): multi-transaction filings are aggregated to the DOMINANT
-- transaction code by total dollar value (shares summed, price = weighted
-- average). code honesty: only P (open-market buy) and S (open-market sale)
-- are headline-worthy; A/M/G/F/… are grants/exercises/gifts/withholding and
-- are labeled as such, never sold as conviction trades.
CREATE TABLE IF NOT EXISTS insider_trades (
  accession TEXT PRIMARY KEY,          -- Form 4 accession number
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  insider   TEXT NOT NULL DEFAULT '',  -- reporting owner name
  title     TEXT NOT NULL DEFAULT '',  -- officer title / Director / 10% owner
  code      TEXT NOT NULL DEFAULT '',  -- dominant transaction code (P/S/A/M/…)
  shares    REAL NOT NULL DEFAULT 0,   -- total shares in the dominant code
  price     REAL NOT NULL DEFAULT 0,   -- weighted-average price per share
  value     REAL NOT NULL DEFAULT 0,   -- shares * price (dollar value)
  tx_ts     INTEGER NOT NULL DEFAULT 0,-- transaction date, unix seconds
  filed_ts  INTEGER NOT NULL DEFAULT 0 -- filing time (lags the trade ~2 business days)
);
CREATE INDEX IF NOT EXISTS idx_insider_sym_ts ON insider_trades (symbol_id, filed_ts DESC);
CREATE INDEX IF NOT EXISTS idx_insider_ts ON insider_trades (filed_ts DESC);

-- inst_holdings: parsed 13F-HR information tables for a curated list of
-- notable managers (hardcoded CIKs in internal/ingest/edgar). Quarterly data
-- with up to a 45-day legal lag. symbol_id is a BEST-EFFORT name match from
-- the issuer name; unmatched rows keep symbol_id NULL (honest — CUSIP→ticker
-- has no free authoritative map). value is as reported on the filing (USD).
CREATE TABLE IF NOT EXISTS inst_holdings (
  cik       TEXT NOT NULL,             -- manager CIK (zero-padded not required)
  manager   TEXT NOT NULL DEFAULT '',  -- manager display name
  period    TEXT NOT NULL,             -- report period YYYY-MM-DD
  symbol_id INTEGER,                   -- best-effort matched symbol (NULL = unmatched)
  cusip     TEXT NOT NULL,
  name      TEXT NOT NULL DEFAULT '',  -- issuer name as filed
  value     REAL NOT NULL DEFAULT 0,   -- position value as reported (USD)
  shares    REAL NOT NULL DEFAULT 0,   -- shares/principal amount as reported
  PRIMARY KEY (cik, period, cusip)
);
CREATE INDEX IF NOT EXISTS idx_inst_sym ON inst_holdings (symbol_id, period DESC);
CREATE INDEX IF NOT EXISTS idx_inst_cik ON inst_holdings (cik, period DESC);

-- dilution_flags: derived per-symbol dilution signal. level is descriptive,
-- not a prediction: high = dilution-shaped filing (S-1/S-3/424B) in the last
-- 180d AND shares outstanding up >2%; elevated = exactly one of the two;
-- low = neither. reasons is a JSON array of plain-English evidence strings.
CREATE TABLE IF NOT EXISTS dilution_flags (
  symbol_id  INTEGER PRIMARY KEY REFERENCES symbols(id),
  level      TEXT NOT NULL DEFAULT 'low' CHECK (level IN ('low','elevated','high')),
  reasons    TEXT NOT NULL DEFAULT '[]',
  updated_ts INTEGER NOT NULL DEFAULT 0
);

-- ─────────────────────────────────────────────────────────────────────────
-- SIGNAL8 WAVE — STAGE 2: CONGRESSIONAL TRADES (appended block — do not merge
-- into the sections above). Public-domain STOCK Act disclosures (Senate eFD /
-- House Clerk) ingested via the free community Stock Watcher mirrors.
-- HONESTY: disclosures LAG 30-45 DAYS BY LAW — never real-time; amounts are
-- the RANGES reported on the disclosure, not exact values. id is a
-- deterministic content hash, so INSERT OR IGNORE makes every re-download of
-- the (cumulative) mirror dumps idempotent. symbol_id is set only when the
-- disclosed ticker is one we track; unknown tickers keep NULL (honest) while
-- the raw ticker text is preserved in symbol.
CREATE TABLE IF NOT EXISTS congress_trades (
  id           TEXT PRIMARY KEY,           -- sha256-derived content hash
  chamber      TEXT NOT NULL,              -- 'senate' | 'house'
  member       TEXT NOT NULL DEFAULT '',   -- senator / representative as disclosed
  symbol       TEXT NOT NULL DEFAULT '',   -- disclosed ticker (uppercased, sanitized)
  symbol_id    INTEGER,                    -- matched symbols.id (NULL = not tracked here)
  tx_type      TEXT NOT NULL DEFAULT '',   -- purchase | sale_full | sale_partial | sale | exchange | …
  amount_range TEXT NOT NULL DEFAULT '',   -- disclosed range ("$1,001 - $15,000"), never exact
  tx_ts        INTEGER NOT NULL DEFAULT 0, -- transaction date (0 = unparseable on the disclosure)
  disclosed_ts INTEGER NOT NULL DEFAULT 0  -- filing date (lags the trade 30-45d by law)
);
CREATE INDEX IF NOT EXISTS idx_congress_sym_ts ON congress_trades (symbol, tx_ts DESC);
CREATE INDEX IF NOT EXISTS idx_congress_ts ON congress_trades (tx_ts DESC, disclosed_ts DESC);
CREATE INDEX IF NOT EXISTS idx_congress_member ON congress_trades (member, tx_ts DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- SIGNAL8 WAVE — STAGE 3: ANOMALY LAYER (appended block — do not merge into
-- the sections above). Trade-imbalance + unusual-volatility/volume detections
-- from internal/anomaly. HONESTY: every row is a DESCRIPTIVE statistic — a
-- z-score of recent activity vs the SAME symbol's own trailing baseline — not
-- a prediction. detail always states the window + baseline it was measured
-- against; the stock "imbalance" kind is a volume-side PROXY (up-volume vs
-- down-volume on 1m bars) because no true order book exists on free stock
-- data, and its detail says so. hour_bucket = ts/3600: the dedup key that
-- keeps a persisting condition to ONE open anomaly per (symbol, kind) per
-- hour via INSERT OR IGNORE (same idempotency pattern as idx_alerts_dedup).
CREATE TABLE IF NOT EXISTS anomalies (
  id          INTEGER PRIMARY KEY,
  symbol_id   INTEGER NOT NULL REFERENCES symbols(id),
  ts          INTEGER NOT NULL,     -- detection instant (last bar/snap ts), unix seconds
  kind        TEXT NOT NULL CHECK (kind IN ('anomaly_imbalance','anomaly_vol','anomaly_volume')),
  z           REAL NOT NULL,        -- signed z-score (or stated ratio — detail says which)
  detail      TEXT NOT NULL DEFAULT '',
  hour_bucket INTEGER NOT NULL      -- ts/3600, the per-hour dedup bucket
);
CREATE INDEX IF NOT EXISTS idx_anomalies_ts ON anomalies (ts DESC);
CREATE INDEX IF NOT EXISTS idx_anomalies_sym_ts ON anomalies (symbol_id, ts DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_anomalies_dedup
  ON anomalies (symbol_id, kind, hour_bucket);

-- ── dashboard performance (visual-compact wave) ─────────────────────────
-- Covering index for timeframe-led scans: the bars PK leads with symbol_id,
-- so tf='1d' window queries (LastTwoDailyCloses, LastNDailyCloses sparks,
-- movers/heatmap/breadth) otherwise walk EVERY bar row (~29s cold dashboard
-- on a ~470k-row table). (tf, symbol_id, ts, close) makes those scans
-- index-only over just the daily rows.
CREATE INDEX IF NOT EXISTS idx_bars_tf_sym_ts ON bars (tf, symbol_id, ts, close);

-- ─────────────────────────────────────────────────────────────────────────
-- SIGNAL8 WAVE — STAGE 5: COMPANIES DIRECTORY (appended block — do not merge
-- into the sections above). The full SEC-registered company map from the FREE
-- EDGAR file www.sec.gov/files/company_tickers_exchange.json (~10.4k rows,
-- fields cik/name/ticker/exchange — shape verified live 2026-07-04), refreshed
-- by the companies-sync worker in ONE request per 24h run. sic/sic_desc are
-- enriched OPPORTUNISTICALLY from the submissions responses the filings-poller
-- ALREADY fetches for universe symbols (zero added request volume), so they
-- fill in over sweeps and stay '' (honest absence) until covered.
-- ticker is the PK exactly as the SEC file is keyed: one company (one CIK) may
-- appear under several tickers (share classes) — that mirrors the source.
-- exchange may be '' (the SEC lists some registrants with a null exchange);
-- the API/UI renders it as "—", never a guess.
CREATE TABLE IF NOT EXISTS companies (
  cik        INTEGER NOT NULL,
  ticker     TEXT PRIMARY KEY,
  name       TEXT NOT NULL DEFAULT '',
  exchange   TEXT NOT NULL DEFAULT '',
  sic        TEXT NOT NULL DEFAULT '',
  sic_desc   TEXT NOT NULL DEFAULT '',
  updated_ts INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_companies_cik ON companies (cik);
CREATE INDEX IF NOT EXISTS idx_companies_exchange ON companies (exchange);
CREATE INDEX IF NOT EXISTS idx_companies_sic_desc ON companies (sic_desc);

-- ─────────────────────────────────────────────────────────────────────────
-- NEWS SCOPE WAVE (appended block — do not merge into the sections above).
-- No DDL change: this documents a new VALUE for news.sentiment. In addition
-- to bullish|bearish|neutral|unrated (see the news table above), a row may be
-- 'skipped' — an honest TERMINAL label meaning "deliberately not rated":
-- the symbol was outside the news-fetch scope (streamed hot set + top-ranked
-- + user-watchlisted), so no LLM budget is spent on it. 'skipped' differs
-- from 'unrated' ("still pending tagging"): skipped rows are excluded from
-- the UnratedNews queue AND from every sentiment aggregate
-- (sentiment_daily, NewsSentimentAgg), so they can neither inflate the
-- pending backlog nor dilute means with their default 0 score. Applied once
-- by the meta-gated backlog cleanup (meta key 'news_scope_skip_v1') and to
-- nothing else; rated rows are never relabeled.

-- ─────────────────────────────────────────────────────────────────────────
-- STAGE 5 — FINRA REG SHO DAILY SHORT SALE VOLUME (appended block — do not
-- merge into the sections above). Free, registration-less FINRA data
-- (cdn.finra.org/equity/regsho/daily/CNMSshvolYYYYMMDD.txt, verified live
-- 2026-07-06), UNIVERSE-SCOPED: the finra-shorts worker stores rows ONLY for
-- symbols we track. Volumes are REAL because the live files carry FRACTIONAL
-- share volumes (fractional-share trades). short_pct is the derived daily
-- short sale volume ratio short_vol/total_vol (0 when total_vol=0).
-- HONESTY: this ratio is NOT short interest — it includes market-maker
-- liquidity provision, and a high ratio is NOT directly bearish. The API and
-- UI carry that caveat verbatim wherever the number appears.
CREATE TABLE IF NOT EXISTS short_volume (
  symbol_id    INTEGER NOT NULL REFERENCES symbols(id),
  day          TEXT NOT NULL,            -- trade date, YYYY-MM-DD
  short_vol    REAL NOT NULL,
  short_exempt REAL NOT NULL,
  total_vol    REAL NOT NULL,
  short_pct    REAL NOT NULL,            -- short_vol/total_vol (0 when total 0)
  PRIMARY KEY (symbol_id, day)
);
CREATE INDEX IF NOT EXISTS idx_short_volume_day ON short_volume (day DESC);

-- TRADINGVIEW WEBHOOK SIGNALS (appended block — do not merge into the sections
-- above). Inbound Pine Script alert webhooks (POST /api/tv-webhook, shared-secret
-- authed). This is the ONE legitimate TradingView integration: TradingView pushes
-- alert events to us; we never pull their licensed market data. Each row is one
-- received alert. symbol_id is resolved best-effort from the raw ticker when we
-- track it (NULL otherwise); the raw payload is always kept for provenance.
CREATE TABLE IF NOT EXISTS tv_signals (
  id        INTEGER PRIMARY KEY,
  symbol_id INTEGER REFERENCES symbols(id), -- resolved from ticker, NULL if untracked/unresolvable
  ticker    TEXT NOT NULL DEFAULT '',       -- raw ticker as TradingView sent it (e.g. "NASDAQ:NVDA")
  action    TEXT NOT NULL DEFAULT '',       -- free-text: buy | sell | long | short | alert | ...
  price     REAL,                           -- trigger price if the alert included one
  message   TEXT NOT NULL DEFAULT '',       -- human-readable alert message / strategy comment
  raw       TEXT NOT NULL DEFAULT '',       -- full raw JSON payload (provenance, capped)
  ts        INTEGER NOT NULL,               -- receive time (unix seconds)
  seen      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_tv_signals_ts ON tv_signals (ts DESC);
-- Supports the per-symbol firing roll-up (GROUP BY symbol_id, ticker) that
-- GET /api/tv-status runs on every LIVE-tab poll; without it the aggregate is a
-- full-table scan whose cost grows unbounded as tv_signals accumulates.
CREATE INDEX IF NOT EXISTS idx_tv_signals_symbol_ticker ON tv_signals (symbol_id, ticker);

-- ─────────────────────────────────────────────────────────────────────────
-- SIGNALS-HUB OVERHAUL — COMPOSITE SIGNALSCORE (appended block — do not merge
-- into the sections above). One row per (symbol, pass ts, horizon): the forced
-- 1-10 curve score over the cross-section of latest calibrated predictions
-- (see internal/composite). score is a RANK on a fixed distribution (top 5% =
-- 10 … bottom 5% = 1), NOT a probability; curve_pct is the cross-sectional
-- percentile of the edge; edge = calibrated P(up,1d) − 0.5 from the LATEST
-- stored prediction (never recomputed). payload is the full evidence JSON
-- (composite.Payload: factor tiles w/ verdicts+gates+skill chips, additive
-- ledger). HONESTY: the composite-scorer worker stores NOTHING when fewer
-- than 30 symbols have usable predictions — a forced curve over a thin
-- cross-section would fabricate extremes.
CREATE TABLE IF NOT EXISTS composite_scores (
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  ts        INTEGER NOT NULL,          -- pass timestamp, unix seconds
  horizon   TEXT NOT NULL,             -- '1d' (the edge's prediction horizon)
  score     INTEGER NOT NULL,          -- forced-curve 1..10
  curve_pct REAL NOT NULL,             -- cross-sectional percentile of edge, 0..100
  edge      REAL NOT NULL,             -- calibrated P(up) − 0.5 at the pass
  payload   TEXT NOT NULL,             -- JSON: composite.Payload (factors + ledger)
  PRIMARY KEY (symbol_id, ts, horizon)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_composite_scores_ts ON composite_scores (ts DESC);

-- SIGNALS-hub overhaul review fix (appended): the composite-scorer's
-- LatestPredictionsForScoring runs a MAX(ts) GROUP BY symbol_id WHERE horizon=?
-- every 10 minutes; the predictions PK leads (symbol_id, horizon, ts), so that
-- access pattern was a full-table SCAN (~370ms at 1.3M rows, growing unbounded
-- since predictions are never pruned). This covering index serves it directly.
CREATE INDEX IF NOT EXISTS idx_predictions_horizon_sym_ts
  ON predictions (horizon, symbol_id, ts DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- TRADINGVIEW SCANNER RATINGS (appended block — do not merge into the sections
-- above). TradingView's OWN technical-analysis RATING for our tracked symbols,
-- pulled from its PUBLIC scanner endpoint (scanner.tradingview.com/{screener}/
-- scan — no account, no key; verified live 2026-07-07) by the tv-rating worker.
-- HONESTY: reco_* are TradingView's OWN descriptive TA scores (each in [-1,1])
-- on DELAYED data — an EXTERNAL, independent signal, NOT SignalDeck's model and
-- NOT advice. label is Label(reco_all) (Strong Buy … Strong Sell). The API/UI
-- carry that caveat verbatim.

-- tv_exchange caches each stock symbol's EXCHANGE:SYMBOL exchange, resolved via
-- TradingView's public symbol-search endpoint (our symbols table has no
-- exchange). One row per tracked symbol; refreshed incrementally so we never
-- hardcode NASDAQ (DRAM/SNXX resolve to CBOE). resolved_at is when we last
-- resolved it.
CREATE TABLE IF NOT EXISTS tv_exchange (
  symbol_id   INTEGER PRIMARY KEY REFERENCES symbols(id),
  exchange    TEXT NOT NULL,
  resolved_at INTEGER NOT NULL
);

-- tv_ratings is the append-only rating time series: one row per (symbol, pass
-- ts). reco_* / rsi / close_px are nullable (a scanner row may omit a metric),
-- but the worker only writes rows for symbols the scanner returned, so present
-- rows carry real numbers. Never pruned — the external-rating history only grows.
CREATE TABLE IF NOT EXISTS tv_ratings (
  symbol_id  INTEGER NOT NULL,
  ts         INTEGER NOT NULL,
  reco_all   REAL,
  reco_ma    REAL,
  reco_other REAL,
  rsi        REAL,
  close_px   REAL,
  label      TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (symbol_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_tv_ratings_ts ON tv_ratings (ts DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- SELF-AUDIT / DRIFT WATCHDOG (appended block — do not merge into the sections
-- above). The self-audit worker MEASURES the platform's own honesty from
-- resolved history (calibration drift, factor-IC sign flips, prediction bias)
-- once per UTC day and records each finding here so it is queryable and
-- graphable over time (GET /api/self-audit + /api/model-evolution). Append-only
-- time series; every check carries a status so gates render as reasons.
--   status ∈ ok | degrading | over_confident | under_confident | sign_flip
--            | insufficient   (n<30 — measured too thin to judge, never alarmed)
--   metric e.g. 'calibration:1d' | 'prediction_bias:1w' | 'factor_ic:pressure'
--   symbol_id is nullable (fleet-level checks store NULL).
CREATE TABLE IF NOT EXISTS self_audit (
  id        INTEGER PRIMARY KEY,
  ts        INTEGER NOT NULL,
  metric    TEXT NOT NULL,
  symbol_id INTEGER,
  value     REAL NOT NULL DEFAULT 0,
  status    TEXT NOT NULL,
  detail    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_self_audit_metric_ts ON self_audit (metric, ts DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- MODEL-EVOLUTION: ADAPTIVE-WEIGHT HISTORY (appended block). The adaptive-
-- weights worker only persists its LATEST weights in meta (adaptive_weights:v1),
-- overwriting each run — so there was no history to chart. weight_history is the
-- append-only per-run snapshot the worker now writes (one row per non-empty
-- (regime cell, leg)), the source series behind GET /api/model-evolution. Honest
-- by construction: gaps where no learned weight existed, no interpolation.
CREATE TABLE IF NOT EXISTS weight_history (
  id     INTEGER PRIMARY KEY,
  ts     INTEGER NOT NULL,
  regime TEXT NOT NULL,   -- the adaptive cell name ('all' or a regime label)
  leg    TEXT NOT NULL,   -- ensemble leg name
  weight REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_weight_history_ts ON weight_history (ts DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- DATA-EXPANSION WAVE (appended block — do not merge into the sections
-- above). Seven new FREE, keyless external context sources, ALL stored and
-- served as DESCRIPTIVE context with explicit caveats — NOTHING here becomes
-- a scored factor in this wave.

-- short_interest: FINRA's BI-MONTHLY short interest (settlement-dated 15th /
-- EOM files, published ~9 business days after settlement; verified live
-- 2026-07-10). UNIVERSE-SCOPED: the finra-shortint worker stores rows ONLY
-- for symbols we track. Unlike short_volume (daily short-sale VOLUME ratio),
-- this IS actual short interest — but it is ~2 weeks stale by publication.
-- HONESTY: settlement-dated, published ~2wks lagged — descriptive
-- positioning, not advice. The API carries that caveat verbatim.
CREATE TABLE IF NOT EXISTS short_interest (
  symbol_id       INTEGER NOT NULL REFERENCES symbols(id),
  settlement_date TEXT NOT NULL,        -- YYYY-MM-DD (15th or EOM)
  short_qty       REAL NOT NULL,        -- current short position (shares)
  prev_qty        REAL NOT NULL,        -- previous period's short position
  adv             REAL NOT NULL,        -- average daily volume (shares)
  days_to_cover   REAL NOT NULL,        -- FINRA's own days-to-cover
  change_pct      REAL NOT NULL,        -- FINRA's period-over-period change %
  PRIMARY KEY (symbol_id, settlement_date)
);
CREATE INDEX IF NOT EXISTS idx_short_interest_settle ON short_interest (settlement_date DESC);

-- crypto_perp: perp funding + open interest snapshots from Hyperliquid's free
-- public info API (one POST per pass; arrays index-aligned; verified live
-- 2026-07-10). HONESTY: ONE venue (a DEX) — a venue-specific positioning
-- proxy, descriptive only. The API carries that caveat verbatim.
CREATE TABLE IF NOT EXISTS crypto_perp (
  symbol_id     INTEGER NOT NULL REFERENCES symbols(id),
  ts            INTEGER NOT NULL,       -- snapshot time, unix seconds
  funding       REAL NOT NULL,          -- current hourly funding rate
  open_interest REAL NOT NULL,          -- open interest, coin units
  mark_px       REAL NOT NULL,          -- mark price (USD)
  PRIMARY KEY (symbol_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_crypto_perp_ts ON crypto_perp (ts DESC);

-- cot_reports: the CFTC's weekly Commitments of Traders legacy futures-only
-- report (free Socrata API, verified live 2026-07-10), ingested for a small
-- curated set of index/crypto futures contracts. HONESTY: Tuesday positions
-- published Friday (3-day lag) — positioning, NOT prediction. The API carries
-- that caveat verbatim.
CREATE TABLE IF NOT EXISTS cot_reports (
  contract      TEXT NOT NULL,          -- contract_market_name
  report_date   TEXT NOT NULL,          -- YYYY-MM-DD (Tuesday as-of date)
  noncomm_long  REAL NOT NULL,
  noncomm_short REAL NOT NULL,
  comm_long     REAL NOT NULL,
  comm_short    REAL NOT NULL,
  open_interest REAL NOT NULL,
  PRIMARY KEY (contract, report_date)
);
CREATE INDEX IF NOT EXISTS idx_cot_reports_date ON cot_reports (report_date DESC);

-- stocktwits_sentiment: rolling per-symbol tallies of StockTwits' free public
-- symbol stream (verified live 2026-07-10). Each row is a PAGE SNAPSHOT —
-- counts over the ~30 newest messages at fetch time, NOT a complete census.
-- Scope: watchlist + streamed hot set only (news-fetcher-style scoping).
-- HONESTY: retail message sentiment from a self-selected crowd — descriptive
-- only. The API carries that caveat verbatim.
CREATE TABLE IF NOT EXISTS stocktwits_sentiment (
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  ts        INTEGER NOT NULL,           -- snapshot time, unix seconds
  bullish   INTEGER NOT NULL,
  bearish   INTEGER NOT NULL,
  untagged  INTEGER NOT NULL,
  total     INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_stocktwits_ts ON stocktwits_sentiment (ts DESC);

-- wiki_article: the per-symbol Wikipedia article-title resolution CACHE for
-- the attention proxy. ok=1 rows carry the resolved title; ok=0 rows remember
-- a FAILED resolution so the worker never re-hammers Wikimedia for names its
-- heuristic cannot map (honest absence, retried only if the row is deleted).
CREATE TABLE IF NOT EXISTS wiki_article (
  symbol_id   INTEGER PRIMARY KEY REFERENCES symbols(id),
  article     TEXT NOT NULL DEFAULT '', -- resolved title ('' when ok=0)
  ok          INTEGER NOT NULL,         -- 1 resolved / 0 resolution failed
  resolved_at INTEGER NOT NULL          -- unix seconds
);

-- wiki_views: daily Wikipedia page views (agent=user, en.wikipedia) for
-- resolved symbols — the public-attention proxy series. HONESTY: attention is
-- NOT a trading signal, and the article resolution is a heuristic that can
-- pick the wrong page. The API carries that caveat verbatim.
CREATE TABLE IF NOT EXISTS wiki_views (
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  day       TEXT NOT NULL,              -- YYYY-MM-DD
  views     INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, day)
);
CREATE INDEX IF NOT EXISTS idx_wiki_views_day ON wiki_views (day DESC);

-- cboe_pc: CBOE's market-wide daily options put/call ratios + volumes from
-- the free per-day statistics document (probed + verified live 2026-07-10).
-- HONESTY: a market-wide positioning/hedging gauge — index puts are largely
-- hedges, so high index P/C is NOT directly bearish; descriptive only. The
-- API carries that caveat verbatim.
CREATE TABLE IF NOT EXISTS cboe_pc (
  day       TEXT PRIMARY KEY,           -- trade date, YYYY-MM-DD
  total_pc  REAL NOT NULL,
  index_pc  REAL NOT NULL,
  equity_pc REAL NOT NULL,
  vix_pc    REAL NOT NULL,
  call_vol  REAL NOT NULL,
  put_vol   REAL NOT NULL,
  total_vol REAL NOT NULL
);

-- tv_quotes: near-real-time quote tape from TradingView's public scanner
-- (verified live 2026-07-10) — fills the last-15-minutes gap free bar feeds
-- leave (Alpaca SIP is 15m-guarded; IEX is volume-thin). price is the rtc
-- REAL-TIME Cboe One composite when present (realtime=1), else the
-- 15-min-DELAYED close (realtime=0, update_mode-flagged upstream);
-- day_volume is FULL-MARKET cumulative. A QUOTE TAPE, not a record: the
-- tv-quotes worker prunes rows older than ~2h inline every pass — bars are
-- the durable history. HONESTY: descriptive supplement to the bar record,
-- not a replacement; the API labels realtime-vs-delayed per row.
CREATE TABLE IF NOT EXISTS tv_quotes (
  symbol_id     INTEGER NOT NULL REFERENCES symbols(id),
  ts            INTEGER NOT NULL,   -- fetch time, unix seconds
  price         REAL NOT NULL,      -- rtc when present, else delayed close
  delayed_close REAL NOT NULL,      -- the scanner's 15-min-delayed close
  change_pct    REAL NOT NULL,      -- day change %
  day_volume    REAL NOT NULL,      -- full-market cumulative day volume
  realtime      INTEGER NOT NULL,   -- 1 = price is the real-time rtc composite
  PRIMARY KEY (symbol_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_tv_quotes_ts ON tv_quotes (ts DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- NEWS-TRENDS + STRATEGY-LAB wave (appended block).

-- news_trends: per-symbol daily headline-volume trend rows written by the
-- news-trends worker (30m). n is the day's headline count; z is that count
-- standardized against the symbol's OWN trailing-30-day baseline, and is
-- NULL when the honesty gate is not met (fewer than 10 prior days with any
-- news, or a zero-variance baseline) — an absent z is information, never a
-- fabricated 0. HONESTY: a headline-frequency trend — descriptive attention,
-- not a forecast. The API carries that caveat verbatim.
CREATE TABLE IF NOT EXISTS news_trends (
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  day       TEXT NOT NULL,              -- YYYY-MM-DD (UTC)
  n         INTEGER NOT NULL,           -- headlines on that day
  z         REAL,                       -- NULL when the baseline gate fails
  PRIMARY KEY (symbol_id, day)
);
CREATE INDEX IF NOT EXISTS idx_news_trends_day ON news_trends (day DESC);

-- strategy_results: per-(symbol,strategy) walk-forward backtest results for
-- the STRATEGY LAB — 8 classic PUBLISHED strategies (golden cross, Donchian,
-- Connors RSI-2, Jegadeesh-Titman momentum, MACD, Bollinger mean-reversion,
-- 52w-high breakout, absolute dual momentum) replayed by the strategy-lab
-- worker through the bias-free next-bar-fill backtester on OUR OWN ~2y daily
-- bars with explicit costs. cagr_reported mirrors the engine's CAGRReported
-- honesty flag (CAGR below the span/trade floor must not be shown);
-- win_rate_ok mirrors WinRateMeaningful. HONESTY: in-sample history on our
-- bars, not live performance and not advice. The API carries that verbatim.
CREATE TABLE IF NOT EXISTS strategy_results (
  symbol_id     INTEGER NOT NULL REFERENCES symbols(id),
  strategy      TEXT NOT NULL,
  ts            INTEGER NOT NULL,        -- run time, unix seconds
  total_return  REAL NOT NULL,
  cagr          REAL NOT NULL,           -- only show when cagr_reported=1
  sharpe        REAL NOT NULL,
  max_dd        REAL NOT NULL,
  win_rate      REAL NOT NULL,           -- only show when win_rate_ok=1
  n_trades      INTEGER NOT NULL,
  cagr_reported INTEGER NOT NULL,        -- engine CAGRReported honesty flag
  win_rate_ok   INTEGER NOT NULL,        -- engine WinRateMeaningful flag
  n_bars        INTEGER NOT NULL,        -- bars the backtest actually saw
  PRIMARY KEY (symbol_id, strategy)
);
CREATE INDEX IF NOT EXISTS idx_strategy_results_strategy ON strategy_results (strategy);

-- ─────────────────────────────────────────────────────────────────────────
-- CROSS-SECTIONAL ALPHA wave (appended block).

-- alphax_models: ONE row per horizon — the pooled cross-sectional model
-- (trained over the WHOLE universe at once, predicting RELATIVE
-- outperformance vs the same-day cross-section median, NOT absolute
-- direction) with its latest PURGED WALK-FORWARD out-of-sample grade and
-- gate state. model_json serializes the exact model that earned the grade
-- (provenance). gated=1 means OOS lift <= 0: the model is stored for the
-- record but NEVER blended and NEVER displayed as a signal — per-symbol
-- scores (model_forecasts, model='alphax') exist only while gated=0.
-- HONESTY: backtested, not a live track record. The API carries that
-- caveat verbatim.
CREATE TABLE IF NOT EXISTS alphax_models (
  horizon    TEXT PRIMARY KEY,          -- '1d' / '1w'
  ts         INTEGER NOT NULL,          -- grade time, unix seconds
  oos_lift   REAL NOT NULL,             -- accuracy - base rate (<=0 => gated)
  oos_auc    REAL NOT NULL,
  oos_acc    REAL NOT NULL,
  base_rate  REAL NOT NULL,             -- majority-class floor
  n_train    INTEGER NOT NULL,          -- pooled labeled rows the model saw
  n_test     INTEGER NOT NULL,          -- strictly out-of-sample scored rows
  gated      INTEGER NOT NULL,          -- 1 = no measured edge: never shown as signal
  model_json TEXT NOT NULL              -- serialized {featureKeys, gbm model}
);

-- ─────────────────────────────────────────────────────────────────────────
-- ADVERSARIAL-REVIEW FIXES wave (appended block).

-- idx_features_h_ts: LabeledFeaturesAll (the 6h alphax pooled-trainer read)
-- filters features by horizon (+ version range) and orders by ts DESC with a
-- LIMIT; the only prior index leads with symbol_id, so every pass full-scanned
-- and sorted the whole features table. This index serves the filter AND the
-- order, letting SQLite walk it newest-first and stop at the limit. (L2)
CREATE INDEX IF NOT EXISTS idx_features_h_ts ON features (horizon, ts DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- CANDLESTICK-PATTERNS wave (appended block).

-- pattern_stats: ONE row per (symbol, pattern, horizon) — the MEASURED edge of
-- a candlestick pattern on THIS symbol's OWN history, computed by the
-- pattern-stats worker from ~2y of daily bars (internal/candles.MeasureEdges).
-- hit_rate is the share of the pattern's historical firings whose forward
-- `horizon`-bar move went the pattern's way (bias-aligned); mean_fwd is the
-- mean forward return; n is the sample the stats rest on. Only DIRECTIONAL
-- patterns are stored, and only when n >= MinPatternN (=15) — a thin sample is
-- withheld, never shown as an edge. HONESTY: a measured tendency on this
-- instrument's own past, descriptive and not advice; the API carries that
-- caveat verbatim.
CREATE TABLE IF NOT EXISTS pattern_stats (
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  pattern   TEXT NOT NULL,           -- candles.Pattern.Name (directional only)
  horizon   INTEGER NOT NULL,        -- forward window in bars the edge was measured over
  hit_rate  REAL NOT NULL,           -- share of firings that went the pattern's way
  mean_fwd  REAL NOT NULL,           -- mean forward horizon-bar return (signed, as observed)
  n         INTEGER NOT NULL,        -- historical firings behind the stats (>= 15)
  PRIMARY KEY (symbol_id, pattern, horizon)
);

-- ─────────────────────────────────────────────────────────────────────────
-- RESEARCH DESK — recommendation audit trail (appended block).
-- An append-only, hash-chained record of every DISTINCT recommendation the
-- Desk produces (internal/store/recaudit.go). content_hash makes a rec
-- reproducible (rebuild from the same inputs → same digest); entry_hash chains
-- rows for tamper-evidence exactly like prediction_ledger. WRITE-ONCE: only
-- ever INSERTed. Dedup on (symbol_id, content_hash) keeps polling from growing
-- the chain when a recommendation hasn't changed.
CREATE TABLE IF NOT EXISTS recommendation_audit (
  seq            INTEGER PRIMARY KEY,
  created_at     INTEGER NOT NULL,     -- wall-clock unix seconds at generation
  symbol_id      INTEGER NOT NULL REFERENCES symbols(id),
  symbol         TEXT NOT NULL,
  market         TEXT NOT NULL,
  decision       TEXT NOT NULL,
  confidence     TEXT NOT NULL,
  content_hash   TEXT NOT NULL,        -- reproducibility digest of rec + inputs
  sources        TEXT NOT NULL,        -- JSON array of data sources used
  model_versions TEXT NOT NULL,        -- JSON object of model versions
  assumptions    TEXT NOT NULL,        -- JSON array of stated assumptions
  prev_hash      TEXT NOT NULL,        -- entry_hash of seq-1 ("" for genesis)
  entry_hash     TEXT NOT NULL         -- the chain link
);
CREATE INDEX IF NOT EXISTS idx_recaudit_symbol ON recommendation_audit(symbol_id, seq DESC);

-- ─────────────────────────────────────────────────────────────────────────
-- SMART MONEY FACTS wave (appended block — do not merge into the sections
-- above). ONE transparent, decomposed per-symbol "Smart Money Score" plus two
-- event kinds, all built from ALREADY-INGESTED positioning data (SEC Form 4
-- open-market insider trades, FINRA short interest / Reg SHO short volume,
-- crypto perp funding, SEC 13F holdings — see internal/smartmoney). HONESTY:
-- the score is a read of what INFORMED PARTICIPANTS ARE DOING, NOT a price
-- forecast; every component is a bounded factor with its source and the API
-- carries the caveat verbatim (13F quarterly + ~45d lagged; short-volume ratio
-- is not short interest and includes market-maker flow).

-- smart_money_scores: the LATEST score per symbol (upserted every pass). score
-- is the renormalized weighted-mean of the present factors in [-1,1]; label is
-- the descriptive band (strong_accumulation … strong_distribution); payload is
-- the full evidence JSON (factors + raw sub-fields the API re-serves typed).
CREATE TABLE IF NOT EXISTS smart_money_scores (
  symbol_id INTEGER PRIMARY KEY REFERENCES symbols(id),
  ts        INTEGER NOT NULL,
  score     REAL NOT NULL,
  label     TEXT NOT NULL,
  payload   TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_smart_money_score ON smart_money_scores(score);

-- smart_money_events: append-only insider-cluster / squeeze-setup detections.
-- day_bucket (the latest relevant UTC filing/short-vol day) is the dedup key so
-- a persisting condition becomes ONE event per (symbol, kind) per day via the
-- UNIQUE index + INSERT OR IGNORE (same idempotency pattern as anomalies).
CREATE TABLE IF NOT EXISTS smart_money_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  symbol_id  INTEGER NOT NULL REFERENCES symbols(id),
  ts         INTEGER NOT NULL,
  kind       TEXT NOT NULL,   -- insider_cluster | squeeze_setup
  detail     TEXT NOT NULL,
  day_bucket TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sme_dedup ON smart_money_events(symbol_id, kind, day_bucket);

-- ─────────────────────────────────────────────────────────────────────────
-- CONFLUENCE GATE + MONEY SCOREBOARD wave (appended block — do not merge into
-- the sections above). A trade SETUP is only flagged when several INDEPENDENT
-- signal FAMILIES (smart-money, trend, prediction, relative-strength, breakout)
-- AGREE on a direction (see internal/confluence). HONESTY: this manufactures no
-- edge — it is a strict AND over signals that already exist, shown transparently
-- (every family's vote is in the payload). Flagged setups are FORWARD-TRACKED
-- with no lookahead and scored by EXPECTED PROFIT (expectancy/profit-factor),
-- not win rate.

-- confluence_setups: the LATEST assessment per symbol (upserted every pass —
-- all symbols stored, is_setup flags the ones that cleared the gate). payload is
-- the full votes JSON (every present family's dir + reason) the API re-serves.
CREATE TABLE IF NOT EXISTS confluence_setups (
  symbol_id INTEGER PRIMARY KEY REFERENCES symbols(id),
  ts        INTEGER NOT NULL,
  direction INTEGER NOT NULL,
  agree     INTEGER NOT NULL,
  dissent   INTEGER NOT NULL,
  score     REAL NOT NULL,
  is_setup  INTEGER NOT NULL,
  payload   TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_confluence_score ON confluence_setups(score);

-- confluence_outcomes: the FORWARD-TRACKED record of flagged setups. One row per
-- (symbol, ts, horizon); entry_px frozen at flag time; fwd_return / win / resolved_at
-- filled later by the resolver from realized bars (NO lookahead). This is what the
-- money scoreboard grades — by expectancy, not win rate.
CREATE TABLE IF NOT EXISTS confluence_outcomes (
  symbol_id   INTEGER NOT NULL REFERENCES symbols(id),
  ts          INTEGER NOT NULL,
  horizon     TEXT NOT NULL,
  direction   INTEGER NOT NULL,
  agree       INTEGER NOT NULL,
  entry_px    REAL NOT NULL,
  fwd_return  REAL,
  win         INTEGER,
  resolved_at INTEGER,
  PRIMARY KEY(symbol_id, ts, horizon)
);

-- confluence_events: append-only "confluence setup" detections. day_bucket (the
-- setup's UTC day) is the dedup key so a persisting setup becomes ONE event per
-- (symbol, kind) per day via the UNIQUE index + INSERT OR IGNORE.
CREATE TABLE IF NOT EXISTS confluence_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  symbol_id  INTEGER NOT NULL REFERENCES symbols(id),
  ts         INTEGER NOT NULL,
  kind       TEXT NOT NULL,   -- confluence_setup
  detail     TEXT NOT NULL,
  day_bucket TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_confl_evt_dedup ON confluence_events(symbol_id, kind, day_bucket);

-- ─────────────────────────────────────────────────────────────────────────
-- RESEARCH LAB — POSTMORTEM ENGINE (appended block — do not merge above).
-- One row per resolved, WRONG, meaningfully-convicted prediction. The
-- postmortem-runner worker attributes each miss to a ranked failure taxonomy
-- (internal/postmortem) and stores the result so failures can be CLUSTERED and
-- fed back as research. Idempotent: PK is (symbol_id, horizon, ts) = the exact
-- prediction it explains, so a miss is postmortem'd at most once (INSERT OR
-- IGNORE). reasons is the full ranked JSON array; primary/secondary are lifted
-- out for cheap clustering queries. NO lookahead: every input was recorded at
-- or before resolution time.
CREATE TABLE IF NOT EXISTS prediction_postmortems (
  symbol_id   INTEGER NOT NULL,
  horizon     TEXT NOT NULL,
  ts          INTEGER NOT NULL,   -- the prediction's bar ts (unix s)
  prob        REAL NOT NULL,      -- calibrated P(up) at prediction time
  up          INTEGER NOT NULL,   -- realized 1/0
  fwd_return  REAL NOT NULL,      -- realized forward return (signed)
  conviction  REAL NOT NULL,      -- |prob-0.5|
  magnitude   REAL NOT NULL,      -- |fwd_return|
  primary_reason   TEXT NOT NULL,
  secondary_reason TEXT NOT NULL DEFAULT '',
  reasons     TEXT NOT NULL DEFAULT '[]',  -- full ranked JSON
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, horizon, ts)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_postmortem_primary ON prediction_postmortems(primary_reason);
CREATE INDEX IF NOT EXISTS idx_postmortem_created ON prediction_postmortems(created_at);

-- ─────────────────────────────────────────────────────────────────────────
-- RESEARCH LAB — HYPOTHESIS REGISTRY (appended block — do not merge above).
-- One row per candidate feature-rule the Research Lab has promoted to SHADOW.
-- A hypothesis earns a shadow row only after beating the incumbent baseline by a
-- Bonferroni-corrected Wilson lower bound on strict walk-forward OOS data. It is
-- then RE-EVALUATED every run on fresh, accruing data; pass_streak counts
-- consecutive wins, and only a sustained streak flips status to 'promoted'. A
-- run that fails resets the streak; repeated failure flips status to 'rejected'.
-- NOTHING here mutates live predictions — a promoted hypothesis is an advisory,
-- independently-verifiable finding. id is the deterministic spec hash.
CREATE TABLE IF NOT EXISTS research_hypotheses (
  id            TEXT PRIMARY KEY,       -- deterministic hash of the spec
  kind          TEXT NOT NULL,          -- ablation | interaction | row_gate
  spec          TEXT NOT NULL,          -- full Hypothesis JSON
  description   TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,          -- shadow | promoted | rejected
  discovered_at INTEGER NOT NULL,
  base_lift     REAL NOT NULL DEFAULT 0, -- incumbent baseline lift at discovery
  disc_lift     REAL NOT NULL DEFAULT 0, -- candidate OOS lift at discovery
  last_lift     REAL NOT NULL DEFAULT 0, -- most recent OOS lift
  last_wilson   REAL NOT NULL DEFAULT 0, -- most recent corrected Wilson floor
  last_n        INTEGER NOT NULL DEFAULT 0,
  pass_streak   INTEGER NOT NULL DEFAULT 0,
  fail_streak   INTEGER NOT NULL DEFAULT 0,
  evals         INTEGER NOT NULL DEFAULT 0,
  promoted_at   INTEGER NOT NULL DEFAULT 0,
  updated_at    INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_research_status ON research_hypotheses(status);

-- ═══ BAYESIAN RESEARCH LEDGER (internal/researchledger) ═══════════════════
-- Named research beliefs with an auditable evidence chain. Unlike
-- research_hypotheses (the automated micro-hypothesis lab), these are the
-- PROGRAM-level discoveries: prior fixed at creation, posterior recomputed
-- from the full evidence chain on every insert (deterministic — the chain IS
-- the belief). Status is a derived band, never the source of truth.
CREATE TABLE IF NOT EXISTS research_ledger_hypotheses (
  id             TEXT PRIMARY KEY,        -- 'H001', 'H008', ...
  family         TEXT NOT NULL,           -- signal family: momentum|meanrev|...
  statement      TEXT NOT NULL,
  horizon        TEXT NOT NULL DEFAULT '',
  prior          REAL NOT NULL,           -- fixed at creation, never edited
  max_edge       REAL NOT NULL DEFAULT 0.10, -- plausible-edge band for BF
  posterior      REAL NOT NULL,           -- derived: recomputed from evidence
  status         TEXT NOT NULL,           -- derived band (see researchledger)
  replications   INTEGER NOT NULL DEFAULT 0, -- derived from evidence
  contradictions INTEGER NOT NULL DEFAULT 0, -- derived from evidence
  regimes        INTEGER NOT NULL DEFAULT 1, -- distinct vol regimes covered
  open_questions TEXT NOT NULL DEFAULT '[]', -- JSON array of strings
  tradable_form  TEXT NOT NULL DEFAULT '',   -- the position that would earn the money
  economic_test  TEXT NOT NULL DEFAULT '',   -- the run that graded that position net of costs
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS research_ledger_evidence (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  hyp_id      TEXT NOT NULL REFERENCES research_ledger_hypotheses(id),
  ts          INTEGER NOT NULL,
  kind        TEXT NOT NULL,   -- experiment | replication | attack | manual
  k           INTEGER NOT NULL DEFAULT 0,  -- binomial wins (experiment/replication)
  n           INTEGER NOT NULL DEFAULT 0,  -- binomial trials
  p0          REAL NOT NULL DEFAULT 0,     -- naive baseline graded against
  bf          REAL NOT NULL,               -- Bayes factor (re-clamped on read)
  note        TEXT NOT NULL DEFAULT '',
  window_from INTEGER NOT NULL DEFAULT 0,  -- data window graded (unix secs);
  window_to   INTEGER NOT NULL DEFAULT 0   -- replications use disjoint windows
);
CREATE INDEX IF NOT EXISTS idx_rledger_evidence_hyp ON research_ledger_evidence(hyp_id, ts);

-- ── research discovery engine wave (appended block — keep at END of file so
-- parallel schema edits by other agents never collide) ──────────────────────
-- research_weeks: the HISTORICAL evidence base for the research engine — one
-- point-in-time weekly observation per (symbol, calendar week) computed from
-- daily bars (2020→present backfill). vec is the same JSON name->float64 shape
-- as features.vec but computed retrospectively with strict no-lookahead
-- discipline (trailing windows only; label = the exact live outcome-resolver
-- geometry). The independence unit for grading is the calendar WEEK, matching
-- the ledger's week-trial discipline. Rows are recompute-idempotent (REPLACE).
-- NOTE: this is a survivor-universe backtest base (today's active symbols
-- projected into the past) — the ledger levies a standing survivorship attack
-- on every grade drawn from it.
CREATE TABLE IF NOT EXISTS research_weeks (
  symbol_id  INTEGER NOT NULL,
  week       INTEGER NOT NULL,   -- calendar-week bucket = ts / 604800
  ts         INTEGER NOT NULL,   -- anchor daily-bar ts (last bar of the week)
  vec        TEXT NOT NULL,      -- JSON name -> float64, point-in-time features
  fwd_return REAL NOT NULL,      -- forward 1w return (resolver geometry)
  up         INTEGER NOT NULL,   -- fwd_return > 0
  era        TEXT NOT NULL,      -- covid_crash | bull_2020_21 | bear_2022 | ...
  high_vol   INTEGER NOT NULL DEFAULT 0,  -- VIX >= 25 at the anchor
  created_at INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, week)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_research_weeks_week ON research_weeks(week);
CREATE INDEX IF NOT EXISTS idx_research_weeks_era ON research_weeks(era);

-- ═══ VOLATILITY-REGIME FORECASTS (internal/volregime) ════════════════════════
-- The platform's one validated-edge forecast: will a symbol's next-quarter
-- realized volatility be elevated or calm? One row per symbol, overwritten each
-- pass (INSERT OR REPLACE on the symbol_id PK — only the latest read renders).
CREATE TABLE IF NOT EXISTS vol_forecasts (
  symbol_id           INTEGER NOT NULL PRIMARY KEY REFERENCES symbols(id),
  ts                  INTEGER NOT NULL,   -- when computed, unix seconds
  regime              TEXT    NOT NULL,   -- 'elevated' | 'calm'
  conviction          REAL    NOT NULL,   -- [0,1]
  historical_accuracy REAL    NOT NULL,   -- MEASURED walk-forward acc at this tier
  tier                TEXT    NOT NULL,
  rank                REAL    NOT NULL,    -- current vol percentile in trailing window
  n                   INTEGER NOT NULL     -- daily returns the forecast rests on
) WITHOUT ROWID;

-- ═══ MARKET-STRUCTURE REGIME FORECASTS (internal/structregime) ═══════════════
-- The 2026-07-17 alpha-loop winners beyond vol63: trend/liquidity/vol21 regime
-- calls plus gap-fill event forecasts. One row per (symbol, kind), overwritten
-- each pass — only the latest read renders.
CREATE TABLE IF NOT EXISTS regime_forecasts (
  symbol_id           INTEGER NOT NULL REFERENCES symbols(id),
  kind                TEXT    NOT NULL,   -- 'trend21' | 'liquidity21' | 'vol21' | 'gapfill5' | 'trend63' | 'trend21-crypto' | 'liquidity21-crypto'
  ts                  INTEGER NOT NULL,   -- when computed, unix seconds
  horizon_days        INTEGER NOT NULL,
  regime              TEXT    NOT NULL,
  conviction          REAL    NOT NULL,   -- [0,1]
  historical_accuracy REAL    NOT NULL,   -- MEASURED walk-forward acc at this tier
  tier                TEXT    NOT NULL,
  rank                REAL    NOT NULL,
  n                   INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, kind)
) WITHOUT ROWID;

-- ═══ REGIME-FORECAST OUTCOMES — LIVE GRADING (credibility wave) ═══════════════
-- The regime forecasts above are overwritten in place, so on their own they can
-- never be graded: this table FREEZES at most one call per (symbol, kind,
-- UTC-day) — conviction and the CLAIMED accuracy captured at call time, never
-- rewritten — and the regime-outcome worker later fills in the REALIZED regime
-- label recomputed exactly as the engine defines it (internal/structregime
-- resolve helpers). correct/actual stay NULL until enough forward daily bars
-- exist. Idempotency: INSERT OR IGNORE on the (symbol_id, kind, day) unique
-- index, day = ts/86400 (UTC day of the call).
CREATE TABLE IF NOT EXISTS regime_outcomes (
  id                  INTEGER PRIMARY KEY,
  symbol_id           INTEGER NOT NULL REFERENCES symbols(id),
  kind                TEXT    NOT NULL,
  ts                  INTEGER NOT NULL,   -- call time (unix s), frozen
  day                 INTEGER NOT NULL,   -- trading_day(ts) — dedup key part
  horizon_days        INTEGER NOT NULL,
  regime              TEXT    NOT NULL,   -- the call, frozen
  conviction          REAL    NOT NULL,   -- frozen at call time
  historical_accuracy REAL    NOT NULL,   -- CLAIMED accuracy at call time, frozen
  rank                REAL    NOT NULL,
  resolved_at         INTEGER,            -- NULL until graded
  actual              TEXT,               -- realized regime label (NULL until graded)
  correct             INTEGER,            -- 1/0 (NULL until graded)
  -- NAIVE-PERSISTENCE NULL, frozen at call time alongside the call (2026-07-27).
  -- The "nothing changes" guess: the label the CURRENT state already carries at
  -- the call bar (structregime.Naive*At). It exists so a structural predictor can
  -- receive a FAILING verdict — without a baseline the grader could only ever
  -- compare live accuracy to its own backtest claim. Frozen, never recomputed;
  -- graded against `actual` by the same resolver output. NULL = no baseline was
  -- computable at freeze time (honest absence, excluded from the benchmark tally,
  -- never scored as a miss).
  naive_label         TEXT,
  -- CODE REVISION that froze this row (lineage.RevisionStamp): the bare
  -- vcs.revision of the writing binary, that revision plus "+dirty" when it was
  -- built from a modified checkout, or NULL when the binary embedded no
  -- revision. Added 2026-07-27; rows frozen before that keep NULL, which is the
  -- truthful state — the code behind them is not recoverable and cannot be
  -- invented. The grader refuses to publish a verdict from post-epoch rows whose
  -- stamp is dirty, empty, or names a commit this repository does not contain.
  revision            TEXT,
  -- SUPERSEDED BY (2026-08-06, the trading-day fold). NULL = this row is the
  -- independent observation for its (symbol, kind, day); non-NULL = it names the
  -- row that is, and this one does not count.
  --
  -- `day` used to be ts/86400, a UTC-midnight cut. A US extended session closes
  -- 20:00 ET — 00:00Z under EDT, 01:00Z under EST — so the tail of one trading
  -- day landed on the next UTC day and was frozen as a SECOND call. Measured on
  -- the live corpus: 2,817 such pairs, one row at ~21:00Z and its partner at
  -- 01:00–05:00Z, 98.2% agreeing on the regime. They are one observation.
  --
  -- Re-folding `day` collides those pairs on the dedup key, and the obvious
  -- repair — delete the loser — is the wrong one: this table is the
  -- pre-registration audit trail, every column stamped frozen at call time, and
  -- all 2,817 losers are UNRESOLVED. Deleting ungraded forecasts because the
  -- key that admitted them was wrong is a file drawer. So nothing is deleted;
  -- the loser is marked and the dedup index goes partial. The winner is the
  -- EARLIEST call of the trading day, which is exactly the row the INSERT OR
  -- IGNORE would have kept had the fold been right from the start.
  superseded_by       INTEGER REFERENCES regime_outcomes(id)
);
-- PARTIAL: only rows that still count are unique on the key. Superseded rows
-- keep their frozen bytes and sit outside the constraint.
CREATE UNIQUE INDEX IF NOT EXISTS idx_regime_outcomes_dedup
  ON regime_outcomes (symbol_id, kind, day) WHERE superseded_by IS NULL;
CREATE INDEX IF NOT EXISTS idx_regime_outcomes_unresolved
  ON regime_outcomes (resolved_at) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_regime_outcomes_resolved
  ON regime_outcomes (kind, resolved_at) WHERE resolved_at IS NOT NULL;

-- ═══ NULL-QUARANTINE MANIFEST (frozen, non-extendable) ════════════════════════
-- A silent INSERT-OR-IGNORE no-op in the freeze path wrote 1,157 post-amendment
-- regime_outcomes rows with a NULL naive_label before the write-path guard
-- existed. Those rows cannot be repaired: a persistence baseline computed today
-- for a call made weeks ago is hindsight, not a null, so backfilling is refused.
-- They also cannot be waved through, because the startup invariant that every
-- post-amendment structural row carries a matched null is the thing that makes a
-- FAILING verdict reachable.
--
-- So the historical set is FROZEN here, once, exactly as it stood: one row per
-- affected outcome, plus a manifest digest that is appended to the
-- pre-registration hash chain. The set is never extended — the writer refuses to
-- populate a second time, and any change to these rows re-digests, fails the
-- worker's per-tick verification, and appears on the chain as an AMENDMENT.
--
-- Quarantined rows are NOT relabelled and NOT backfilled. They keep their NULL
-- naive_label, keep grading as NO BASELINE, and stay excluded from every
-- structural benchmark denominator. The manifest raises no number; it only makes
-- one historical data event an immutable, publicly-countable fact so the guard
-- can stay absolutely strict for every FUTURE row.
CREATE TABLE IF NOT EXISTS regime_outcome_quarantine (
  outcome_id INTEGER PRIMARY KEY REFERENCES regime_outcomes(id),
  symbol_id  INTEGER NOT NULL,
  kind       TEXT    NOT NULL,
  day        INTEGER NOT NULL,
  frozen_ts  INTEGER NOT NULL   -- when the set was frozen (never the call time)
);
-- Single-row manifest. id is pinned to 1 so a second freeze is a primary-key
-- violation rather than a second opinion.
CREATE TABLE IF NOT EXISTS regime_outcome_quarantine_manifest (
  id        INTEGER PRIMARY KEY CHECK (id = 1),
  digest    TEXT    NOT NULL,   -- sha256 over the canonical member rendering
  n_rows    INTEGER NOT NULL,
  frozen_ts INTEGER NOT NULL
);

-- ═══ REGIME-CALL POSTMORTEMS (credibility wave) ═══════════════════════════════
-- One deterministic plain-English postmortem per HIGH-conviction (>=0.8) regime
-- call that resolved WRONG. Shape differs from prediction_postmortems (no
-- prob/up, no reason taxonomy — a regime miss has one measured story: what was
-- called, what realized, and the honest base rate implied by the CLAIMED
-- accuracy), so it gets its own small table. Idempotent per outcome via the
-- UNIQUE outcome_id (INSERT OR IGNORE).
CREATE TABLE IF NOT EXISTS regime_postmortems (
  id               INTEGER PRIMARY KEY,
  outcome_id       INTEGER NOT NULL UNIQUE REFERENCES regime_outcomes(id),
  symbol_id        INTEGER NOT NULL REFERENCES symbols(id),
  kind             TEXT    NOT NULL,
  ts               INTEGER NOT NULL,   -- the call's ts
  regime           TEXT    NOT NULL,   -- what was called
  conviction       REAL    NOT NULL,
  claimed_accuracy REAL    NOT NULL,   -- claimed at call time (the base-rate input)
  actual           TEXT    NOT NULL,   -- what realized
  key_name         TEXT    NOT NULL,   -- the one measured number behind the miss
  key_value        REAL    NOT NULL,
  narrative        TEXT    NOT NULL,   -- deterministic plain English, never invented
  created_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_regime_postmortems_created
  ON regime_postmortems (created_at DESC);

-- ── SLOW-READ INDEXES (2026-07-24 perf wave) ─────────────────────────────────
-- /api/track-record scanned prediction_outcomes (240k rows) with a temp-B-tree
-- sort on EVERY request (~22s in the pure-Go driver). This partial index serves
-- the exact read — resolved rows of one horizon, newest first — as an index
-- walk with no sort.
CREATE INDEX IF NOT EXISTS idx_predoutcomes_resolved_hts
  ON prediction_outcomes (horizon, ts DESC)
  WHERE resolved_at IS NOT NULL AND up IS NOT NULL;

-- LatestPeriodicFilingAll (the /api/regimes earnings annotation) GROUP-BYs the
-- 710k-row filings table by symbol over 10-Q/10-K rows. Covering index makes
-- both the grouped subquery and the self-join index-only.
CREATE INDEX IF NOT EXISTS idx_filings_form_sym_ts
  ON filings (form, symbol_id, filed_ts DESC);

-- ── SPLIT-CORRUPTION REPAIR (2026-07-24) ─────────────────────────────────────
-- Audit trail for the split-repair worker. Incremental fetches leave stored
-- history on a stale price basis after a split; the worker detects the
-- resulting discontinuity and re-backfills. Recording every attempt (including
-- failures) is what makes a repair that does NOT clear the corruption visible
-- as a repeated row instead of a silent retry loop.
CREATE TABLE IF NOT EXISTS split_repairs (
  id          INTEGER PRIMARY KEY,
  symbol_id   INTEGER NOT NULL REFERENCES symbols(id),
  split_date  TEXT    NOT NULL,   -- UTC date of the worst detected discontinuity
  ratio       TEXT    NOT NULL,   -- nearest known split factor, e.g. "2:1"
  suspects    INTEGER NOT NULL,   -- how many discontinuities the series carried
  ok          INTEGER NOT NULL,   -- 1 = re-backfill succeeded
  err         TEXT    NOT NULL DEFAULT '',
  repaired_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_split_repairs_sym
  ON split_repairs (symbol_id, repaired_at DESC);
CREATE INDEX IF NOT EXISTS idx_split_repairs_at
  ON split_repairs (repaired_at DESC);

-- ── SURVIVORSHIP (2026-07-24) ────────────────────────────────────────────────
-- delisted_at separates a MARKET fact (the symbol stopped trading) from a
-- SUBSCRIPTION decision (active=0, the user stopped watching). Conflating them
-- is what made survivorship bias invisible: research iterated active=1 and
-- silently dropped every name that died. With this, a point-in-time universe
-- can be reconstructed (store.TradableAt) instead of guessed.
-- Added via the idempotent ALTER path in migrate() — see store.go.

-- ── POINT-IN-TIME UNIVERSE (2026-08-04) ──────────────────────────────────────
-- The MATERIALISED denominator every cross-sectional feature ranks against:
-- one row per (UTC day, symbol) the symbol actually traded. TradableAt answers
-- the same question from added_at/delisted_at, but those are stamps about when
-- WE noticed something; this is evidence about what the market did, and the two
-- can be compared precisely because both exist.
--
-- This table existed in the operator's database from the day it was designed
-- and was ABSENT from schema.sql, so every cold clone — every reviewer, every
-- restore, every deployment — got a database without it while the operator's
-- own copy had it (empty). It is here now so the cold case is the tested case.
-- store.RebuildUniverseMembership fills it; see internal/store/pituniverse.go.
CREATE TABLE IF NOT EXISTS universe_membership (
  day       INTEGER NOT NULL,   -- UTC midnight, unix secs, of the OBSERVATION day
  symbol_id INTEGER NOT NULL,
  source    TEXT    NOT NULL,   -- the EVIDENCE, e.g. 'bars-1d'
  PRIMARY KEY (day, symbol_id)
);
CREATE INDEX IF NOT EXISTS idx_universe_membership_sym
  ON universe_membership (symbol_id, day);

-- ── AUTONOMOUS RESEARCH LOOP (2026-07-25) ────────────────────────────────────
-- Every rule the loop tests, including the ones it kills. A search that records
-- only its winners cannot be audited, and the rejections are what stop the same
-- idea being retried forever. status is 'shadow' or 'rejected' — the loop is
-- deliberately unable to write 'promoted', because automation that can put its
-- own output into production is how a p-hacked rule becomes a live position.
CREATE TABLE IF NOT EXISTS research_loop_hypotheses (
  id           TEXT PRIMARY KEY,
  descr        TEXT    NOT NULL,
  status       TEXT    NOT NULL,
  wilson_lower REAL    NOT NULL,
  survives     INTEGER NOT NULL,
  found_at     INTEGER NOT NULL,
  last_seen    INTEGER NOT NULL,
  -- The Bonferroni divisor this rule actually cleared: grid size times every
  -- search the loop has run over this corpus (research_loop_searches). It is
  -- stored per row because the bar RISES every night — correcting for one
  -- night's grid while taking many nights' chances is the multiplicity error
  -- this column exists to make visible after the fact.
  divisor      INTEGER NOT NULL DEFAULT 0,
  -- The rest of the audit record. grid_size and weeks say how wide the search
  -- was and how much history the rule was judged on; rejected_by names the
  -- gate that killed it ('' when it survived), which is what makes a silent
  -- re-test of an already-killed rule detectable; obs_window pins the exact
  -- research_weeks span searched.
  grid_size    INTEGER NOT NULL DEFAULT 0,
  weeks        INTEGER NOT NULL DEFAULT 0,
  rejected_by  TEXT    NOT NULL DEFAULT '',
  obs_window   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_loop_hyp_seen
  ON research_loop_hypotheses (last_seen DESC);
-- NOTE: the index on rejected_by is created in store.go's migration block, NOT
-- here. This file runs BEFORE the ALTER TABLE migrations, so on a live database
-- whose research_loop_hypotheses predates the rejection-ledger wave the column
-- does not exist yet and indexing it here fails the whole schema apply — which
-- is exactly what kept this wave from ever reaching the live daemon.

-- One row per research-loop PASS, including the passes that REFUSED to search.
-- research_loop_hypotheses records what a search judged; this records that the
-- search happened at all, over what corpus, and under which correction. Without
-- it a null result leaves no durable trace — the only record was a free-text
-- line in worker_runs, a table that is pruned — and a well-powered null is the
-- strongest evidence an honest search can produce.
--
-- Deliberately NOT part of the derived-retention tiers in internal/maintain: it
-- is an audit trail rather than derived data, and it grows at one row per day.
CREATE TABLE IF NOT EXISTS research_loop_runs (
  day             TEXT PRIMARY KEY,
  ran_at          INTEGER NOT NULL,
  grid_size       INTEGER NOT NULL,
  divisor         INTEGER NOT NULL,
  corrected_alpha REAL    NOT NULL,
  obs_count       INTEGER NOT NULL,
  obs_ts_from     INTEGER NOT NULL,
  obs_ts_to       INTEGER NOT NULL,
  survivors       INTEGER NOT NULL,
  judged          INTEGER NOT NULL DEFAULT 0,
  refusal_reason  TEXT    NOT NULL DEFAULT '',
  git_rev         TEXT    NOT NULL DEFAULT '',
  -- Worst-week point-in-time coverage of the corpus this pass searched:
  -- symbols carrying a research row that week / symbols that actually printed
  -- a daily bar that week. The pass row already says what was searched and
  -- under which correction; this says how much of the market was in front of
  -- the search. 0 on rows written before the measurement existed means
  -- "unmeasured", not "no coverage".
  corpus_coverage REAL    NOT NULL DEFAULT 0
);

-- One row per (day, rule) JUDGMENT — the append-only half of the rejection
-- ledger. research_loop_hypotheses is keyed on the rule id and upserted, so it
-- can only ever hold the LATEST verdict for a rule: a rule judged and killed on
-- 200 consecutive nights leaves exactly one row there, and "has this dead rule
-- been silently re-tested?" is unanswerable by construction. Here it is a
-- COUNT(*). Nothing in this table is ever updated in place except by a same-day
-- re-run of the same rule, which is the same look, not a new one.
--
-- Also the durable multiplicity source: COUNT(DISTINCT day) is a count of
-- nights the grid was actually searched that no log-retention policy can prune
-- back down, which is what keeps the Bonferroni divisor monotone.
CREATE TABLE IF NOT EXISTS research_loop_judgments (
  day          TEXT    NOT NULL,
  rule_id      TEXT    NOT NULL,
  status       TEXT    NOT NULL,
  wilson_lower REAL    NOT NULL,
  p0           REAL    NOT NULL,
  divisor      INTEGER NOT NULL,
  grid_size    INTEGER NOT NULL,
  weeks        INTEGER NOT NULL,
  rejected_by  TEXT    NOT NULL DEFAULT '',
  obs_window   TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (day, rule_id)
);
CREATE INDEX IF NOT EXISTS idx_loop_judgment_rule
  ON research_loop_judgments (rule_id, day DESC);

-- ── HONESTY-GAP WAVE (2026-07-25) ────────────────────────────────────────────
-- The four tables behind PREDICTION_PROCESS.md's remaining gaps: a forecast
-- return DISTRIBUTION (replacing the binary up/down target), a content hash per
-- dataset slice (so a claim can be shown irreproducible when a provider revises
-- history), and a canary trial record (so a new model version cannot inherit
-- production automatically).

-- One conditional return-distribution forecast per (symbol, horizon). The
-- probabilities are cost-aware: p_up/p_down are P(move clears +/-tau), and
-- p_inside is the no-trade zone the binary target could not express. skill is
-- the pinball-loss skill of the CONDITIONAL forecast against its own
-- climatology; a non-positive skill means conditioning added nothing and the
-- caller is expected to render the climatology instead of claiming an edge.
CREATE TABLE IF NOT EXISTS return_forecasts (
  symbol_id  INTEGER NOT NULL,
  horizon    TEXT    NOT NULL,
  ts         INTEGER NOT NULL,
  regime     TEXT    NOT NULL,  -- the vol regime conditioned on
  n          INTEGER NOT NULL,
  tau        REAL    NOT NULL,
  mean       REAL    NOT NULL,
  sigma      REAL    NOT NULL,
  q10        REAL    NOT NULL,
  q50        REAL    NOT NULL,
  q90        REAL    NOT NULL,
  p_up       REAL    NOT NULL,
  p_down     REAL    NOT NULL,
  p_inside   REAL    NOT NULL,
  edge       REAL    NOT NULL,
  expected_value REAL NOT NULL,
  skill      REAL,               -- NULL until gradeable
  coverage80 REAL,
  graded_n   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (symbol_id, horizon)
) WITHOUT ROWID;

-- Content hash of the exact rows a measurement read. A changed hash inside a
-- previously-recorded range means the provider rewrote history.
CREATE TABLE IF NOT EXISTS dataset_versions (
  symbol_id  INTEGER NOT NULL,
  timeframe  TEXT    NOT NULL,
  first_ts   INTEGER NOT NULL,
  last_ts    INTEGER NOT NULL,
  n          INTEGER NOT NULL,
  hash       TEXT    NOT NULL,
  checked_at INTEGER NOT NULL,
  revisions  INTEGER NOT NULL DEFAULT 0,  -- times history was rewritten
  PRIMARY KEY (symbol_id, timeframe)
) WITHOUT ROWID;

-- Canary trials: one row per model family, holding the incumbent/challenger
-- versions and the last verdict. decision is promote | hold | reject; a trial
-- that has never cleared its floors sits at hold, which is the correct default.
CREATE TABLE IF NOT EXISTS canary_trials (
  model       TEXT    NOT NULL PRIMARY KEY,
  incumbent   TEXT    NOT NULL,
  challenger  TEXT    NOT NULL,
  decision    TEXT    NOT NULL,
  serving     TEXT    NOT NULL,
  reason      TEXT    NOT NULL,
  inc_n       INTEGER NOT NULL,
  inc_acc     REAL    NOT NULL,
  ch_n        INTEGER NOT NULL,
  ch_acc      REAL    NOT NULL,
  ch_lower    REAL    NOT NULL,
  ch_upper    REAL    NOT NULL,
  baseline    REAL    NOT NULL,
  decided_at  INTEGER NOT NULL
) WITHOUT ROWID;

-- ─────────────────────────────────────────────────────────────────────────
-- SENTIMENT-CORRELATION WAVE (appended block — do not merge into the sections
-- above).
--
-- sentiment_features: per-symbol daily sentiment features, keyed by the SESSION
-- ON WHICH THE SENTIMENT WAS ALREADY ACTIONABLE — not by the headline's own
-- calendar day. A headline published at 21:30 UTC lands after the US close and
-- cannot be traded until the next session; keying it to its own day would grant
-- silent lookahead on the majority of articles, which are published outside
-- market hours. `day` is therefore the first trading session strictly after the
-- article timestamp.
--
-- Distinct from sentiment_daily (which the retired directional ensemble
-- consumed): that table keys on the ARTICLE's UTC day, aggregates only
-- LLM-rated rows, and counts neutral rows as zeros. This one is as-of aligned,
-- lexicon-scored (so the whole archive can be scored reproducibly), and counts
-- ONLY headlines that expressed polarity — a factual headline has no sentiment
-- rather than a sentiment of zero.
CREATE TABLE IF NOT EXISTS sentiment_features (
  symbol_id  INTEGER NOT NULL REFERENCES symbols(id),
  day        TEXT    NOT NULL,           -- YYYY-MM-DD, first ACTIONABLE session
  n_polar    INTEGER NOT NULL,           -- headlines that expressed polarity
  n_all      INTEGER NOT NULL,           -- all headlines mapped to this session
  mean_score REAL    NOT NULL,           -- mean of the polar scores, [-1,+1]
  pos        INTEGER NOT NULL DEFAULT 0,
  neg        INTEGER NOT NULL DEFAULT 0,
  hedged     INTEGER NOT NULL DEFAULT 0, -- polar-but-qualified count
  ver        INTEGER NOT NULL,           -- newssent.Version that produced this
  PRIMARY KEY (symbol_id, day)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_sentiment_features_day ON sentiment_features (day);

-- sentiment_corr: the latest study result per (feature, horizon). One row is
-- overwritten in place rather than appended so a re-run cannot masquerade as
-- fresh independent evidence — the same discipline the research loop's
-- hypothesis table uses. payload is the full sentcorr.Result as JSON, including
-- its gate reason when gated.
CREATE TABLE IF NOT EXISTS sentiment_corr (
  feature  TEXT    NOT NULL,   -- mean_score | score_delta | attention
  horizon  INTEGER NOT NULL,   -- forward sessions
  ts       INTEGER NOT NULL,   -- when the study ran
  obs      INTEGER NOT NULL,   -- independent observations studied
  gated    INTEGER NOT NULL,   -- 1 = no verdict claimed
  payload  TEXT    NOT NULL,   -- JSON sentcorr.Result
  PRIMARY KEY (feature, horizon)
) WITHOUT ROWID;

-- news_symbols: the article <-> symbol mapping.
--
-- WHY THIS EXISTS: `news.id` is the provider article id and the PRIMARY KEY, so
-- the table can hold each article exactly once — but a single article routinely
-- tags several tickers ("Apple and Qualcomm settle"). Under the one-row-per-
-- article constraint every symbol but the first loses that headline, and the
-- names most affected are the smaller ones that have the least coverage to
-- begin with. Normalising the mapping into its own table fixes the coverage gap
-- without migrating a primary key on a live table.
--
-- The sentiment score itself stays on `news`: polarity is a property of the
-- TEXT, identical for every symbol the article mentions.
CREATE TABLE IF NOT EXISTS news_symbols (
  news_id   TEXT    NOT NULL,
  symbol_id INTEGER NOT NULL,
  PRIMARY KEY (news_id, symbol_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_news_symbols_sym ON news_symbols (symbol_id);

-- ═══ PRE-REGISTRATION CHAIN (internal/prereg) ═════════════════════════════
-- What each structural predictor CLAIMED, frozen and hash-chained BEFORE its
-- forecasts began resolving. The chain construction matches the prediction
-- ledger: entry_hash = sha256(prev_hash ‖ payload), so a rewritten claim
-- breaks every link after it and the break is found by recomputation rather
-- than by trust. Rows are APPEND-ONLY — an amended claim is a new row, never
-- an update, so a change stays visible as a change.
CREATE TABLE IF NOT EXISTS prereg_records (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  ts         INTEGER NOT NULL,
  kind       TEXT    NOT NULL,
  spec_json  TEXT    NOT NULL,
  spec_hash  TEXT    NOT NULL,
  prev_hash  TEXT    NOT NULL,
  entry_hash TEXT    NOT NULL,
  note       TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_prereg_kind ON prereg_records(kind, seq);

-- ═══ LEDGER ANCHORS (external tamper-evidence — finding C2) ═══════════════
-- The prediction ledger's hash chain proves INTERNAL CONSISTENCY: no stored row
-- was edited, deleted or reordered. It cannot prove ANTERIORITY, because every
-- anchor it had (meta.ledger_verify_checkpoint) lived in the same file it was
-- policing — an operator can drop every row, drop the checkpoint, regenerate a
-- fabricated chain through the same append path, and both the cached verify and
-- the full genesis walk report intact=true (reproduced in
-- store.TestLedger_ChainProvesConsistencyNotAnteriority).
--
-- Each row here is an Ed25519 signature over (created_at, ledger_seq,
-- ledger_count, head_hash), made with a key held OUTSIDE the database (0600
-- file, path from SIGNALDECK_LEDGER_ANCHOR_KEY). Only the PUBLIC key is stored,
-- so verification needs no secret. Regenerating history and still producing an
-- anchor over the old head requires forging a signature.
--
-- The honest limits, stated here so the table is not read as more than it is:
-- nothing before the FIRST anchor is protected, and an operator who holds the
-- signing key can re-sign a fabricated chain — which is why `digest` is
-- published externally, where a third party's timestamp is outside the
-- operator's reach. APPEND-ONLY: rows are only ever INSERTed.
CREATE TABLE IF NOT EXISTS ledger_anchors (
  seq          INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at   INTEGER NOT NULL,   -- unix seconds at signing
  ledger_seq   INTEGER NOT NULL,   -- prediction_ledger.seq of the anchored head
  ledger_count INTEGER NOT NULL,   -- chain length at signing time
  head_hash    TEXT    NOT NULL,   -- prediction_ledger.entry_hash at ledger_seq
  alg          TEXT    NOT NULL,   -- signature scheme (ed25519)
  pub_key      TEXT    NOT NULL,   -- hex public key — verification needs no secret
  sig          TEXT    NOT NULL,   -- hex signature over the canonical message
  digest       TEXT    NOT NULL    -- short publishable digest (message ‖ sig ‖ pubkey)
);
CREATE INDEX IF NOT EXISTS idx_ledger_anchors_seq ON ledger_anchors(ledger_seq);

-- ═══ EVIDENCE ENGINE (internal/evidence) ══════════════════════════════════
-- Every claim the system publishes gets a machine-checkable evidence record
-- that can go STALE and auto-downgrade. A claim is only as good as its most
-- recent validation: `revalidate_by` is a promise, and the nightly sweep
-- (evidence.SweepRunner) enforces it — past-due claims are marked stale and
-- lose one confidence tier, so an unmaintained claim decays toward "weak"
-- instead of silently keeping yesterday's certainty. Refuting evidence
-- (an interval entirely on the wrong side of the claim's baseline) retires
-- the claim outright. Tiers are RULE-JUSTIFIED, not asserted: the store
-- accepts a claim only when its tier is defensible from its own items
-- (evidence.Validate), so a "strong" row always carries a corrected,
-- cluster-robust interval that excludes its null.
CREATE TABLE IF NOT EXISTS evidence_claims (
  id             TEXT    PRIMARY KEY,
  text           TEXT    NOT NULL,
  scope_json     TEXT    NOT NULL,             -- assets/regimes/horizons/date range
  tier           TEXT    NOT NULL,             -- strong|moderate|weak|refuted
  status         TEXT    NOT NULL,             -- active|stale|downgraded|retired
  last_validated INTEGER NOT NULL,             -- unix seconds of last validation
  revalidate_by  INTEGER NOT NULL,             -- unix seconds; past this = stale
  lineage_json   TEXT    NOT NULL,             -- feature keys + model names
  seeded         INTEGER NOT NULL DEFAULT 0,   -- 1 = programmatic seed, labeled
  updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_evidence_claims_status ON evidence_claims(status);

-- One measured piece of support per row. `n_effective` is CLUSTER-ROBUST
-- (raw n / design effect), never a raw row count — the 2026-07-26 re-audit
-- (A1/A2) is why that distinction is load-bearing here.
CREATE TABLE IF NOT EXISTS evidence_items (
  claim_id    TEXT    NOT NULL REFERENCES evidence_claims(id) ON DELETE CASCADE,
  idx         INTEGER NOT NULL,               -- stable order within the claim
  kind        TEXT    NOT NULL,               -- live-record|backtest|study|...
  value       REAL    NOT NULL,               -- the measured statistic
  n_effective REAL    NOT NULL,               -- cluster-robust effective n
  method      TEXT    NOT NULL,               -- walk-forward|purged-walk-forward|live-forward|...
  correction  TEXT    NOT NULL DEFAULT '',    -- multiple-testing correction applied ('' = none)
  ci_low      REAL,                           -- NULL = no interval (thin evidence)
  ci_high     REAL,
  baseline    REAL,                           -- the null the CI is judged against
  source_ref  TEXT    NOT NULL DEFAULT '',    -- file/table the number came from
  PRIMARY KEY (claim_id, idx)
) WITHOUT ROWID;

-- Lineage spine (Layers 2+8)
-- One edge table ties the three hypothesis registries, dataset windows,
-- model legs, ledgered predictions, paper trades and evidence claims into a
-- single traversable graph (lineage.Trace / GET /api/lineage). Node kinds and
-- edge kinds are validated in internal/lineage — the table stores strings so
-- the schema never has to migrate for a new kind. PERMANENT: never pruned by
-- retention (see store/retention.go doctrine comment).
CREATE TABLE IF NOT EXISTS lineage_edges (
  src_kind   TEXT    NOT NULL,               -- feature|dataset_version|hypothesis|experiment|model|prediction|trade|claim
  src_id     TEXT    NOT NULL,
  dst_kind   TEXT    NOT NULL,
  dst_id     TEXT    NOT NULL,
  edge_kind  TEXT    NOT NULL,               -- generated_by|tested_in|produced|traded_as|graded_by|evidenced_by
  created_at INTEGER NOT NULL,
  meta_json  TEXT    NOT NULL DEFAULT '',    -- e.g. {"rev":"<git sha>"} — code version that wrote the edge
  PRIMARY KEY (src_kind, src_id, dst_kind, dst_id, edge_kind)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_lineage_edges_dst ON lineage_edges(dst_kind, dst_id);

-- Decision Engine (Layer 1)
-- ═══ EV DECISIONS (internal/ev) ═══════════════════════════════════════════
-- Every entry/exit verdict the EV gate renders — INCLUDING every DO_NOTHING —
-- so refusals become auditable: "what did not trading cost" is a query, not a
-- shrug (ARCHITECTURE_EV.md Layer 1/Layer 9). `inputs_json` snapshots the full
-- assessment with its has-flags, so a missing input is visibly missing rather
-- than a zero; `net_ev` is NULL when it was not measurable. Symbol text is
-- denormalized at write time so the row stays readable if the universe changes.
CREATE TABLE IF NOT EXISTS ev_decisions (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          INTEGER NOT NULL,   -- decision time (the pass's as-of clock)
  strategy    TEXT    NOT NULL,   -- e.g. flagship-1d
  symbol_id   INTEGER NOT NULL,
  symbol      TEXT    NOT NULL,
  horizon     TEXT    NOT NULL,
  decision    TEXT    NOT NULL,   -- BUY | SELL | DO_NOTHING
  reason      TEXT    NOT NULL,   -- enumerated ev.Reason label
  net_ev      REAL,               -- NULL = unmeasurable (never a silent zero)
  rank        INTEGER NOT NULL DEFAULT 0, -- 1-based net-EV rank within the pass (0 = unranked)
  rank_of     INTEGER NOT NULL DEFAULT 0, -- candidates assessed in the pass
  inputs_json TEXT    NOT NULL    -- full ev.Assessment snapshot incl. has-flags
);
CREATE INDEX IF NOT EXISTS idx_ev_decisions_ts ON ev_decisions(ts DESC);
CREATE INDEX IF NOT EXISTS idx_ev_decisions_symbol ON ev_decisions(symbol_id, ts DESC);

-- Prediction attribution (Layer 6)
-- Top-N named parts of one ledgered prediction's RAW blended probability:
-- each row is a probability-delta from the neutral 0.5 prior (parts across a
-- seq sum to raw_prob - 0.5 before top-N truncation). kind is
-- component|gbm_feature|leg; method records how the parts were derived
-- ('saabas' = Saabas path attribution for GBM feature parts — an approximation
-- with disclosed depth-interaction bias, NOT exact SHAP).
CREATE TABLE IF NOT EXISTS prediction_attributions (
  ledger_seq   INTEGER NOT NULL,   -- prediction_ledger.seq this explains
  rank         INTEGER NOT NULL,   -- 0 = largest |contribution|
  symbol_id    INTEGER NOT NULL,
  horizon      TEXT    NOT NULL,
  ts           INTEGER NOT NULL,   -- the prediction's bar ts
  name         TEXT    NOT NULL,   -- comp_* / gbm_<feature> / leg name
  kind         TEXT    NOT NULL,   -- component | gbm_feature | leg
  contribution REAL    NOT NULL,   -- probability delta from the 0.5 prior
  method       TEXT    NOT NULL DEFAULT 'saabas',
  PRIMARY KEY (ledger_seq, rank)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_pred_attr_symbol
  ON prediction_attributions(symbol_id, horizon, ledger_seq);

-- ── Publication verdicts (2026-08-04 fix pack) ──────────────────────────
--
-- One row per (predictor, horizon, variant) per grading evaluation. Append-only
-- history: the latest row by evaluated_at is the current publication state.
--
-- STICKINESS. A model the record has once contradicted stays contradicted. The
-- obvious implementation — an UPDATE trigger guarding retired 1->0 — protects
-- nothing here, because nothing updates: every grade INSERTs a new row, so an
-- un-retire arrives as a fresh retired=0 row the UPDATE trigger never sees. The
-- guard therefore has to run on INSERT and consult the history, which is what
-- publication_verdicts_no_unretire does. It is a backstop under
-- publication.BuildVerdict, not a substitute for it: the builder is what knows
-- WHY a row is retired, and the trigger is what makes forgetting impossible.
CREATE TABLE IF NOT EXISTS publication_verdicts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  predictor TEXT NOT NULL,
  horizon   TEXT NOT NULL,
  variant   TEXT NOT NULL DEFAULT '',
  publication_status TEXT NOT NULL CHECK (publication_status IN
    ('OK','INSUFFICIENT','FAILED','RETIRED','REFUSED_STALE','NO_BASELINE','QUARANTINED')),
  retired           INTEGER NOT NULL DEFAULT 0 CHECK (retired IN (0,1)),
  retirement_sticky INTEGER NOT NULL DEFAULT 0 CHECK (retirement_sticky IN (0,1)),
  retire_reason     TEXT,
  retirement_source TEXT,             -- 'evidence' | 'grader' | 'history' | 'wilson'
  evidence_claim_id TEXT,
  current_n_eff           REAL,
  current_distinct_blocks INTEGER,
  ci_method TEXT,
  ci_lower  REAL,
  ci_upper  REAL,
  null_rate REAL,
  skill_pp  REAL,
  grader_sha256 TEXT,
  reasons_json       TEXT NOT NULL DEFAULT '[]',
  evidence_refs_json TEXT NOT NULL DEFAULT '[]',
  evaluated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (predictor, horizon, variant, evaluated_at)
);
CREATE INDEX IF NOT EXISTS idx_publication_verdicts_latest
  ON publication_verdicts (predictor, horizon, variant, evaluated_at DESC);

-- The only way past this is to delete history, which is itself the loud act.
CREATE TRIGGER IF NOT EXISTS publication_verdicts_no_unretire
BEFORE INSERT ON publication_verdicts
WHEN new.retired = 0 AND EXISTS (
  SELECT 1 FROM publication_verdicts p
   WHERE p.predictor = new.predictor
     AND p.horizon   = new.horizon
     AND p.variant   = new.variant
     AND p.retired   = 1
)
BEGIN
  SELECT RAISE(ABORT, 'retirement is sticky and cannot be cleared');
END;

-- Grader heartbeats. A scheduled task exiting 0 is NOT evidence the grade ran:
-- on 2026-08-03 the task reported success while the grader had been refusing
-- for 33 hours. success here means the grader produced a registry, and
-- finished_at is what staleness is measured against.
CREATE TABLE IF NOT EXISTS grader_heartbeats (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task    TEXT NOT NULL,
  success INTEGER NOT NULL CHECK (success IN (0,1)),
  finished_at TEXT NOT NULL,
  grader_sha256  TEXT,
  rows_evaluated INTEGER,
  error TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_grader_heartbeats_task_finished
  ON grader_heartbeats (task, finished_at DESC);
