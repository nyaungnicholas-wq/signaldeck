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
