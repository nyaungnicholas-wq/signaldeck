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
  detail      TEXT NOT NULL DEFAULT ''
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
  entry_hash    TEXT    NOT NULL    -- sha256(prev_hash ‖ canonical-json of this entry) — the chain link
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
