// HONESTY-GAP WAVE (2026-07-25) — persistence for the four gaps
// PREDICTION_PROCESS.md left open: distributional return forecasts, dataset
// version hashes, and canary trials. (The feature-redundancy report is a single
// fleet-wide document and lives in meta, like the adaptive weights.)
//
// Reads use the pooled s.db handle; writes go through s.w, matching the rest of
// the store's write discipline.
package store

import (
	"context"
	"database/sql"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// MetaFeatureRedundancy is the meta key holding the latest fleet-wide
// feature-redundancy report (JSON).
const MetaFeatureRedundancy = "feature_redundancy:v1"

// MetaPriceValidation is the meta key holding the latest second-source price
// validation summary (JSON). Only derived comparison statistics are stored —
// never the second provider's prices.
const MetaPriceValidation = "price_validation:v1"

// ReturnForecast is one stored conditional return-distribution forecast.
type ReturnForecast struct {
	SymbolID      int64      `json:"-"`
	Symbol        string     `json:"symbol,omitempty"`
	Market        string     `json:"market,omitempty"`
	Horizon       md.Horizon `json:"horizon"`
	Ts            int64      `json:"ts"`
	Regime        string     `json:"regime"`
	N             int        `json:"n"`
	Tau           float64    `json:"tau"`
	Mean          float64    `json:"mean"`
	Sigma         float64    `json:"sigma"`
	Q10           float64    `json:"q10"`
	Q50           float64    `json:"q50"`
	Q90           float64    `json:"q90"`
	PUp           float64    `json:"pUp"`
	PDown         float64    `json:"pDown"`
	PInside       float64    `json:"pInside"`
	Edge          float64    `json:"edge"`
	ExpectedValue float64    `json:"expectedValue"`
	// Skill/Coverage80 are nil until enough forecasts have resolved to grade
	// the conditioning against its climatology.
	Skill      *float64 `json:"skill"`
	Coverage80 *float64 `json:"coverage80"`
	GradedN    int      `json:"gradedN"`
}

// UpsertReturnForecast writes one symbol+horizon's latest distribution in place.
func (s *Store) UpsertReturnForecast(ctx context.Context, f ReturnForecast) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO return_forecasts
		  (symbol_id, horizon, ts, regime, n, tau, mean, sigma, q10, q50, q90,
		   p_up, p_down, p_inside, edge, expected_value, skill, coverage80, graded_n)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id, horizon) DO UPDATE SET
		  ts=excluded.ts, regime=excluded.regime, n=excluded.n, tau=excluded.tau,
		  mean=excluded.mean, sigma=excluded.sigma, q10=excluded.q10,
		  q50=excluded.q50, q90=excluded.q90, p_up=excluded.p_up,
		  p_down=excluded.p_down, p_inside=excluded.p_inside, edge=excluded.edge,
		  expected_value=excluded.expected_value, skill=excluded.skill,
		  coverage80=excluded.coverage80, graded_n=excluded.graded_n`,
		f.SymbolID, string(f.Horizon), f.Ts, f.Regime, f.N, f.Tau, f.Mean, f.Sigma,
		f.Q10, f.Q50, f.Q90, f.PUp, f.PDown, f.PInside, f.Edge, f.ExpectedValue,
		f.Skill, f.Coverage80, f.GradedN)
	return err
}

// DeleteReturnForecasts removes a symbol's distributions — called when the
// predictor REFUSES (thin or contaminated data), so a stale forecast is never
// left standing as if it were current. Honest absence beats a stale number.
func (s *Store) DeleteReturnForecasts(ctx context.Context, symbolID int64) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM return_forecasts WHERE symbol_id=?`, symbolID)
	return err
}

// ReturnForecasts returns the stored distributions, newest first, joined to
// symbol names. horizon and symbol filters are optional (empty = all).
func (s *Store) ReturnForecasts(ctx context.Context, horizon, symbol string, limit int) ([]ReturnForecast, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, s.symbol, s.market, f.horizon, f.ts, f.regime, f.n, f.tau,
		       f.mean, f.sigma, f.q10, f.q50, f.q90, f.p_up, f.p_down, f.p_inside,
		       f.edge, f.expected_value, f.skill, f.coverage80, f.graded_n
		FROM return_forecasts f
		JOIN symbols s ON s.id = f.symbol_id
		WHERE (?='' OR f.horizon=?) AND (?='' OR s.symbol=?)
		ORDER BY f.ts DESC, s.symbol ASC
		LIMIT ?`, horizon, horizon, symbol, symbol, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ReturnForecast
	for rows.Next() {
		var f ReturnForecast
		var h string
		var skill, cov sql.NullFloat64
		if err := rows.Scan(&f.SymbolID, &f.Symbol, &f.Market, &h, &f.Ts, &f.Regime,
			&f.N, &f.Tau, &f.Mean, &f.Sigma, &f.Q10, &f.Q50, &f.Q90, &f.PUp, &f.PDown,
			&f.PInside, &f.Edge, &f.ExpectedValue, &skill, &cov, &f.GradedN); err != nil {
			return nil, err
		}
		f.Horizon = md.Horizon(h)
		if skill.Valid {
			v := skill.Float64
			f.Skill = &v
		}
		if cov.Valid {
			v := cov.Float64
			f.Coverage80 = &v
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DatasetVersion is one stored dataset-slice hash.
type DatasetVersion struct {
	SymbolID  int64  `json:"-"`
	Symbol    string `json:"symbol,omitempty"`
	Timeframe string `json:"timeframe"`
	FirstTs   int64  `json:"firstTs"`
	LastTs    int64  `json:"lastTs"`
	N         int    `json:"n"`
	Hash      string `json:"hash"`
	CheckedAt int64  `json:"checkedAt"`
	Revisions int    `json:"revisions"`
}

// DatasetVersion reads the stored hash for one slice. ok=false when none exists
// yet (the first observation of a slice can never be a revision).
func (s *Store) DatasetVersion(ctx context.Context, symbolID int64, tf string) (DatasetVersion, bool, error) {
	var v DatasetVersion
	err := s.db.QueryRowContext(ctx, `
		SELECT symbol_id, timeframe, first_ts, last_ts, n, hash, checked_at, revisions
		FROM dataset_versions WHERE symbol_id=? AND timeframe=?`, symbolID, tf).
		Scan(&v.SymbolID, &v.Timeframe, &v.FirstTs, &v.LastTs, &v.N, &v.Hash, &v.CheckedAt, &v.Revisions)
	if err == sql.ErrNoRows {
		return DatasetVersion{}, false, nil
	}
	if err != nil {
		return DatasetVersion{}, false, err
	}
	return v, true, nil
}

// UpsertDatasetVersion records a slice's current hash. revisions is a running
// count the caller increments only when history was actually rewritten — a pure
// extension is not a revision.
func (s *Store) UpsertDatasetVersion(ctx context.Context, v DatasetVersion) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO dataset_versions
		  (symbol_id, timeframe, first_ts, last_ts, n, hash, checked_at, revisions)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id, timeframe) DO UPDATE SET
		  first_ts=excluded.first_ts, last_ts=excluded.last_ts, n=excluded.n,
		  hash=excluded.hash, checked_at=excluded.checked_at,
		  revisions=excluded.revisions`,
		v.SymbolID, v.Timeframe, v.FirstTs, v.LastTs, v.N, v.Hash, v.CheckedAt, v.Revisions)
	return err
}

// DatasetVersions lists stored hashes, most recently checked first.
func (s *Store) DatasetVersions(ctx context.Context, limit int) ([]DatasetVersion, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT v.symbol_id, s.symbol, v.timeframe, v.first_ts, v.last_ts, v.n,
		       v.hash, v.checked_at, v.revisions
		FROM dataset_versions v JOIN symbols s ON s.id = v.symbol_id
		ORDER BY v.revisions DESC, v.checked_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []DatasetVersion
	for rows.Next() {
		var v DatasetVersion
		if err := rows.Scan(&v.SymbolID, &v.Symbol, &v.Timeframe, &v.FirstTs, &v.LastTs,
			&v.N, &v.Hash, &v.CheckedAt, &v.Revisions); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CanaryTrial is one model family's promotion trial.
type CanaryTrial struct {
	Model      string  `json:"model"`
	Incumbent  string  `json:"incumbent"`
	Challenger string  `json:"challenger"`
	Decision   string  `json:"decision"`
	Serving    string  `json:"serving"`
	Reason     string  `json:"reason"`
	IncN       int     `json:"incN"`
	IncAcc     float64 `json:"incAcc"`
	ChN        int     `json:"chN"`
	ChAcc      float64 `json:"chAcc"`
	ChLower    float64 `json:"chLower"`
	ChUpper    float64 `json:"chUpper"`
	Baseline   float64 `json:"baseline"`
	DecidedAt  int64   `json:"decidedAt"`
}

// UpsertCanaryTrial records the latest verdict for a model family.
func (s *Store) UpsertCanaryTrial(ctx context.Context, t CanaryTrial) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO canary_trials
		  (model, incumbent, challenger, decision, serving, reason,
		   inc_n, inc_acc, ch_n, ch_acc, ch_lower, ch_upper, baseline, decided_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(model) DO UPDATE SET
		  incumbent=excluded.incumbent, challenger=excluded.challenger,
		  decision=excluded.decision, serving=excluded.serving, reason=excluded.reason,
		  inc_n=excluded.inc_n, inc_acc=excluded.inc_acc, ch_n=excluded.ch_n,
		  ch_acc=excluded.ch_acc, ch_lower=excluded.ch_lower, ch_upper=excluded.ch_upper,
		  baseline=excluded.baseline, decided_at=excluded.decided_at`,
		t.Model, t.Incumbent, t.Challenger, t.Decision, t.Serving, t.Reason,
		t.IncN, t.IncAcc, t.ChN, t.ChAcc, t.ChLower, t.ChUpper, t.Baseline, t.DecidedAt)
	return err
}

// CanaryTrials lists every trial, most recently decided first.
func (s *Store) CanaryTrials(ctx context.Context) ([]CanaryTrial, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT model, incumbent, challenger, decision, serving, reason,
		       inc_n, inc_acc, ch_n, ch_acc, ch_lower, ch_upper, baseline, decided_at
		FROM canary_trials ORDER BY decided_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []CanaryTrial
	for rows.Next() {
		var t CanaryTrial
		if err := rows.Scan(&t.Model, &t.Incumbent, &t.Challenger, &t.Decision, &t.Serving,
			&t.Reason, &t.IncN, &t.IncAcc, &t.ChN, &t.ChAcc, &t.ChLower, &t.ChUpper,
			&t.Baseline, &t.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// VersionedOutcome is one resolved prediction joined to the FEATURE VERSION that
// produced it. The feature version IS the model version here: a change to the
// vector is a change to the model, so grouping resolved outcomes by it gives a
// real incumbent-vs-challenger split with no new plumbing.
type VersionedOutcome struct {
	Version int
	Ts      int64
	Up      bool
	Correct bool
}

// VersionedOutcomes returns resolved outcomes for one horizon, deduplicated to
// ONE observation per (symbol, UTC day) — the independent-observation
// discipline every accuracy surface here uses, because pooling intraday rows
// inflates n roughly sixtyfold. The kept row per symbol-day is the latest.
//
// sinceTs bounds the evidence window (0 = the whole record). It is an explicit
// parameter rather than a default because the two callers legitimately want
// different windows and the difference is load-bearing: a HEAD-TO-HEAD grade
// between two model versions can use the whole record, since survivorship
// contamination hits both arms alike, while any gate comparing an ABSOLUTE
// record against an absolute null must start at GradingEpoch (the survivorship epoch until the 2026-09-20 window re-registration) — that
// comparison is precisely the one the contamination distorts.
func (s *Store) VersionedOutcomes(ctx context.Context, h md.Horizon, limit int, sinceTs int64) ([]VersionedOutcome, error) {
	if limit <= 0 {
		limit = 100000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.version, o.ts, o.up, o.prob
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		JOIN (
		  SELECT symbol_id, settle_day(o2.settle_ts, o2.ts) AS day, MAX(o2.ts) AS mts
		  FROM prediction_outcomes o2
		  WHERE o2.horizon=? AND o2.resolved_at IS NOT NULL AND o2.up IS NOT NULL
		    AND o2.ts >= ?
		  GROUP BY symbol_id, day
		) d ON d.symbol_id=o.symbol_id AND d.mts=o.ts
		WHERE f.horizon=? AND o.resolved_at IS NOT NULL AND o.up IS NOT NULL
		  AND o.ts >= ?
		ORDER BY o.ts DESC LIMIT ?`, string(h), sinceTs, string(h), sinceTs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []VersionedOutcome
	for rows.Next() {
		var vo VersionedOutcome
		var up int
		var prob float64
		if err := rows.Scan(&vo.Version, &vo.Ts, &up, &prob); err != nil {
			return nil, err
		}
		vo.Up = up == 1
		// A probability of exactly 0.5 is not a call; count it as incorrect
		// rather than crediting a coin flip to the model.
		vo.Correct = (prob > 0.5) == vo.Up && prob != 0.5
		out = append(out, vo)
	}
	return out, rows.Err()
}
