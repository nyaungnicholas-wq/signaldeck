package store

import (
	"context"
	"database/sql"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Prediction is a stored calibrated ensemble prediction.
type Prediction struct {
	SymbolID   int64      `json:"-"`
	Symbol     string     `json:"symbol,omitempty"`
	Horizon    md.Horizon `json:"horizon"`
	Ts         int64      `json:"ts"`
	RawProb    float64    `json:"rawProb"`
	CalProb    float64    `json:"calProb"`
	NUsed      int        `json:"nUsed"`
	Components string     `json:"components"`
	// Weights is the per-leg weight map actually used for THIS blend, JSON
	// encoded. Empty means the blend fell through to the equal-weight prior.
	//
	// Components already records what each leg SAID; without the weights
	// nothing records how much each leg was BELIEVED, so "the ensemble was
	// weighted by measured skill" and "the ensemble fell back to a static
	// prior" are indistinguishable after the fact. That distinction is the
	// difference between a weighting bug and an absent-evidence problem.
	Weights string `json:"weights,omitempty"`
	// Basis names WHICH tier supplied those weights — "personal",
	// "regime:<cell>", "global" or "static". The adaptive layer's fallback
	// chain is the thing most likely to be silently carrying the fleet, and a
	// per-leg audit that cannot group by it is reading a mixture.
	Basis string `json:"basis,omitempty"`
}

// BasisEpoch identifies the LABEL-AND-SIGNAL BASIS that produced an outcome
// row. Every row this build seeds carries it, so a grader can tell two
// populations apart instead of pooling them and reporting the mixture.
//
// It is a hand-bumped constant, NOT time.Now(). A per-row timestamp would give
// every row a distinct value and group nothing; the whole point is that rows
// sharing a basis share a number. Bump it when — and only when — a change makes
// new rows non-comparable with old ones. The resolver settlement guard is the
// worked example: it stopped labeling 1d rows against a forward bar whose
// session had not closed, so rows written after it are not the same measurement
// as rows written before it, and 44% vs anything across that line is a mixture.
//
// The value is the UTC instant this basis took effect (2026-08-07 00:00Z) — the
// first full UTC day under the settlement-guarded resolver and the ranking gate.
// Rows written by earlier builds stay NULL, which reads as "basis predates the
// marker" and is the honest answer: nothing retroactively knows which build
// wrote them.
//
// Stamping alone EXCLUDES NOTHING. There is no reader yet, so every grader still
// sees every row; a grader that wants one basis must say so itself. That is
// deliberate — changing what the SHA-pinned grader counts is a pre-registration
// change, not a code change.
const BasisEpoch int64 = 1786060800

// UpsertPrediction stores a prediction and seeds its outcome row.
func (s *Store) UpsertPrediction(ctx context.Context, p Prediction) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO predictions (symbol_id, horizon, ts, raw_prob, cal_prob, n_used, components, weights, basis)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		p.SymbolID, string(p.Horizon), p.Ts, p.RawProb, p.CalProb, p.NUsed, p.Components,
		p.Weights, p.Basis); err != nil {
		return err
	}
	// NUsed==0 means NO leg was admitted, so this row is EVIDENCE, not a
	// forecast: its components record what each leg said, and nothing else
	// about it is a prediction. It therefore gets no outcome row.
	//
	// prediction_outcomes is the population every grader reads — the accuracy
	// registry, the calibration fit, the live record — and the grader is
	// SHA-pinned on the pre-registration chain. Seeding a legless 0.5 there
	// would land it on one side of the 0.5 threshold and be scored as a
	// confident directional call; 1,202 live rows already carried exactly that
	// shape. Enforcing it HERE rather than at the caller makes it structural: a
	// legless row cannot reach the graded population by any code path.
	//
	// Readers of the predictions table filter on n_used > 0 for the same
	// reason, so an evidence row is never served as a forecast either.
	if p.NUsed > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO prediction_outcomes (symbol_id, horizon, ts, prob, basis_epoch)
			VALUES (?,?,?,?,?)`, p.SymbolID, string(p.Horizon), p.Ts, p.CalProb, BasisEpoch); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestPrediction returns the newest prediction for a symbol+horizon.
func (s *Store) LatestPrediction(ctx context.Context, symbolID int64, h md.Horizon) (Prediction, bool, error) {
	p := Prediction{SymbolID: symbolID, Horizon: h}
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, raw_prob, cal_prob, n_used, components FROM predictions
		WHERE symbol_id=? AND horizon=? AND n_used > 0
		ORDER BY ts DESC LIMIT 1`,
		symbolID, string(h)).Scan(&p.Ts, &p.RawProb, &p.CalProb, &p.NUsed, &p.Components)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	return p, err == nil, err
}

// UnresolvedPredictions returns pending prediction outcomes at/before cutoff
// that CAN still be graded — the symbol has at least one daily bar at or after
// the row's target instant.
//
// The existence check is what keeps the queue moving. Rows are ordered oldest
// first and taken in batches, and 992 rows belonged to symbols whose bars had
// stopped entirely (WBA, PARA, MRO, JNPR and other delisted tickers). Those can
// never satisfy the grading predicate, yet being the oldest they occupied two
// thirds of every batch on every 10-minute pass, so resolvable rows behind them
// were reached slowly or not at all — classic head-of-line blocking, and the
// dead set only grows.
//
// They are skipped, never resolved and never deleted: the outcome for a symbol
// that stopped printing bars is genuinely unknown, and inventing one (or
// dropping the row) would quietly improve the measured record.
func (s *Store) UnresolvedPredictions(ctx context.Context, h md.Horizon, cutoff, horizonSecs int64, limit int) ([]struct {
	SymbolID int64
	Ts       int64
	Prob     float64
}, error,
) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.symbol_id, p.ts, p.prob FROM prediction_outcomes p
		WHERE p.resolved_at IS NULL AND p.horizon=? AND p.ts<=?
		  AND EXISTS (SELECT 1 FROM bars b
		              WHERE b.symbol_id = p.symbol_id AND b.tf='1d'
		                AND b.ts >= p.ts + ?)
		ORDER BY p.ts LIMIT ?`,
		string(h), cutoff, horizonSecs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []struct {
		SymbolID int64
		Ts       int64
		Prob     float64
	}
	for rows.Next() {
		var r struct {
			SymbolID int64
			Ts       int64
			Prob     float64
		}
		if err := rows.Scan(&r.SymbolID, &r.Ts, &r.Prob); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolvePrediction records the realized up/down outcome.
//
// settle_ts — the base bar this row is graded from, and the unit of independent
// evidence every day-clustered statistic folds on (md.SettleDay) — is stamped
// HERE, at the moment the row is graded, rather than being left for
// BackfillSettleTs to fill on a later pass. Leaving it NULL was not a
// correctness bug (the fold falls back to the calendar day) but it was a
// permanent lag: the newest rows are exactly the ones the live published numbers
// lean on, and they were the ones still folding on the wrong unit. The
// derivation is deliberately identical to BackfillSettleTs's — newest 1d bar at
// or before the prediction — so the two agree by construction; if no such bar
// exists it stays NULL and the fold degrades honestly.
func (s *Store) ResolvePrediction(ctx context.Context, symbolID int64, h md.Horizon, ts int64, fwdReturn float64) error {
	up := 0
	if fwdReturn > 0 {
		up = 1
	}
	_, err := s.w.ExecContext(ctx, `
		UPDATE prediction_outcomes SET up=?, fwd_return=?, resolved_at=?,
		  settle_ts = (
		    SELECT MAX(b.ts) FROM bars b
		    WHERE b.symbol_id = prediction_outcomes.symbol_id
		      AND b.tf = '1d' AND b.ts <= prediction_outcomes.ts
		  )
		WHERE symbol_id=? AND horizon=? AND ts=?`,
		up, fwdReturn, time.Now().Unix(), symbolID, string(h), ts)
	return err
}

// ResolvedPredictionPairs returns (PUBLISHED prob, up) pairs — the calibrated
// probability frozen at prediction time against its realized outcome. This is
// the GRADING view: it answers "are our 70% calls actually 70%?" about the
// number users saw.
//
// Do NOT fit a recalibration map on it. The map is applied to the RAW blend
// probability, and prob here is the map's own previous output, so fitting on
// it is both a coordinate error and a recursion (2026-07-26 review, C3). Use
// ResolvedRawPredictionPairs to fit; use this to grade.
func (s *Store) ResolvedPredictionPairs(ctx context.Context, h md.Horizon, limit int) (probs []float64, ups []float64, err error) {
	rows, qerr := s.db.QueryContext(ctx, `
		SELECT prob, up FROM prediction_outcomes
		WHERE resolved_at IS NOT NULL AND horizon=? ORDER BY ts DESC LIMIT ?`,
		string(h), limit)
	if qerr != nil {
		return nil, nil, qerr
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var p float64
		var u int
		if err := rows.Scan(&p, &u); err != nil {
			return nil, nil, err
		}
		probs = append(probs, p)
		ups = append(ups, float64(u))
	}
	return probs, ups, rows.Err()
}

// ResolvedRawPredictionPairs returns (RAW blend prob, realized up, UTC day)
// triples for fitting the recalibration map — the newest `limit` INDEPENDENT
// resolved outcomes for one horizon, one row per (symbol, UTC day).
//
// It exists because ResolvedPredictionPairs returns prediction_outcomes.prob,
// which UpsertPrediction seeds from CalProb: fitting a map on that column and
// then applying the map to raw is a coordinate error, and since cal_prob is the
// map's own previous output it also makes the fit recursive rather than out of
// sample (2026-07-26 review, C3). A map applied to raw must be fit on raw, so
// this joins predictions.raw_prob to the resolved outcome.
//
// Only resolved, non-voided rows are returned (resolved_at and up both NOT
// NULL), so a still-open prediction can never train the map that will be
// applied to it.
//
// ONE ROW PER (SYMBOL, TRADING DAY) — the row-count fix that mattered most.
// The prediction runner re-scores the same symbol many times a day (measured
// 2026-08-04: 15,781 rows across 329 symbols and 149 timestamps = 48 rows per
// symbol per day). Every one of those rows predicts the SAME forward move and
// resolves to the SAME label, so counting them as separate training pairs
// inflates the apparent sample by the re-score rate and by the cross-section at
// once. Measured consequence: the newest 3,000 raw pairs spanned TWO calendar
// days, so the fleet-wide map was fit on ~2 independent market moves, memorised
// their direction, and was then applied to a fresh day. Live cost was 17
// accuracy points (raw 53% -> calibrated 36%) and up-calls collapsing to 1-19%
// of symbols on days when 65-74% of symbols rose.
//
// The dedup rule is deliberately the SAME one the accuracy registry already
// grades with — one observation per (symbol, horizon, day), newest wins — so the
// surface that FITS the map and the surface that GRADES it can never disagree
// about what one observation is. That mismatch was the whole bug: the registry
// had already been corrected, the calibration fit had not.
//
// The day is md.TradingDay via the trading_day() SQLite function, NOT ts/86400.
// A US extended session closes at 20:00 ET — 00:00Z under EDT — so a UTC-midnight
// fold splits one session in two and counts its tail as a second independent
// observation. Measured across the graded record: 16,323 UTC-day buckets against
// 15,394 trading-day buckets, so 929 were phantoms.
//
// The returned days are real trading-day numbers, not ordinals, so the caller can
// split a holdout on a day boundary and count distinct days before deciding it
// has enough evidence to fit anything.
func (s *Store) ResolvedRawPredictionPairs(ctx context.Context, h md.Horizon, limit int) (raws []float64, ups []float64, days []int64, err error) {
	rows, qerr := s.db.QueryContext(ctx, `
		SELECT raw_prob, up, day FROM (
			SELECT p.raw_prob AS raw_prob, o.up AS up, trading_day(o.ts) AS day,
			       ROW_NUMBER() OVER (
			         PARTITION BY o.symbol_id, trading_day(o.ts)
			         ORDER BY o.ts DESC
			       ) AS rn
			FROM prediction_outcomes o
			JOIN predictions p
			  ON p.symbol_id=o.symbol_id AND p.horizon=o.horizon AND p.ts=o.ts
			WHERE o.resolved_at IS NOT NULL AND o.up IS NOT NULL AND o.horizon=?
		)
		WHERE rn=1
		ORDER BY day DESC LIMIT ?`,
		string(h), limit)
	if qerr != nil {
		return nil, nil, nil, qerr
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var p float64
		var u int
		var d int64
		if err := rows.Scan(&p, &u, &d); err != nil {
			return nil, nil, nil, err
		}
		raws = append(raws, p)
		ups = append(ups, float64(u))
		days = append(days, d)
	}
	return raws, ups, days, rows.Err()
}

// ── prequential-majority benchmark ──────────────────────────────────────

// SeedBenchmarkOutcome seeds an outcome row for a BENCHMARK pseudo-predictor
// (a namespaced horizon such as "1d#pm"). Benchmarks bypass the predictions
// table on purpose: they exist to be graded, never displayed, and must not
// enter calibration fits, dashboards or the ledger — every reader of
// predictions/prediction_outcomes filters on exact horizon values, so the
// namespaced horizon keeps benchmark rows out of all of those by construction
// while the registry's per-horizon grouping picks them up automatically.
func (s *Store) SeedBenchmarkOutcome(ctx context.Context, symbolID int64, h md.Horizon, ts int64, prob float64) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO prediction_outcomes (symbol_id, horizon, ts, prob, basis_epoch)
		VALUES (?,?,?,?,?)`, symbolID, string(h), ts, prob, BasisEpoch)
	return err
}

// PrequentialMajorityProb returns the hindsight-free constant guess a
// majority-follower would commit RIGHT NOW for horizon h: 1 when the
// deduplicated resolved record over UTC days strictly before beforeDay runs
// majority-up, 0 when majority-down. ok=false when the record is empty or
// exactly tied — there is no majority to follow, so the caller commits NOTHING.
//
// ok exists because the previous contract returned a bare 0.5 for that case and
// documented it as grading like a constant "up" guess under a >= 0.5 rule. The
// graders actually threshold at `prob > 0.5`, so 0.5 was scored as a constant
// DOWN call — the opposite of the documented behaviour, and a directional claim
// the null never made. An abstention has no honest encoding on a [0,1]
// probability axis that every reader thresholds; the only correct move is not to
// write the row.
//
// Dedup mirrors DirectionalRecord and the accuracy registry: one row per
// (symbol, UTC-day), keeping the day's latest. sinceTs bounds the evidence
// window (the survivorship epoch — a majority learned from survivor-seeded rows
// would be a null in name only).
func (s *Store) PrequentialMajorityProb(ctx context.Context, h md.Horizon, beforeDay, sinceTs int64) (float64, bool, error) {
	q := `
	WITH dedup AS (
	  SELECT up, ROW_NUMBER() OVER (PARTITION BY symbol_id, trading_day(ts) ORDER BY ts DESC) rn
	  FROM prediction_outcomes
	  WHERE horizon = ? AND resolved_at IS NOT NULL AND up IS NOT NULL
	    AND ts >= ? AND trading_day(ts) < ?
	)
	SELECT COUNT(*), COALESCE(SUM(up),0) FROM dedup WHERE rn = 1`
	var n, ups int
	if err := s.db.QueryRowContext(ctx, q, string(h), sinceTs, beforeDay).Scan(&n, &ups); err != nil {
		return 0.5, false, err
	}
	switch {
	case n == 0 || ups*2 == n:
		// NO MAJORITY TO FOLLOW — ok=false, and the caller must publish nothing.
		//
		// This used to return 0.5 and the caller stored it as a benchmark row.
		// Every grader in the tree calls a prediction at `prob > 0.5`, so an
		// exact 0.5 is scored as a confident DOWN call, not as an abstention.
		// Measured live: all 4,004 "1w#pm" rows carried prob=0.5 — the 1w
		// majority-follower had never once found a majority — and the registry
		// duly graded the platform's NAIVE BASELINE as a unanimous always-short
		// strategy scoring 48.75%. The number the ensemble was being compared
		// against was measuring something nobody had implemented.
		return 0.5, false, nil
	case ups*2 > n:
		return 1, true, nil
	default:
		return 0, true, nil
	}
}

// ── regime ──────────────────────────────────────────────────────────────

// UpsertRegime stores the latest regime for a symbol and logs a change row
// when the label differs from the previously-stored one.
func (s *Store) UpsertRegime(ctx context.Context, symbolID, ts int64, label string, strength float64, note string) error {
	var prev string
	err := s.db.QueryRowContext(ctx, `SELECT label FROM regime_state WHERE symbol_id=?`, symbolID).Scan(&prev)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO regime_state (symbol_id, ts, label, strength, note) VALUES (?,?,?,?,?)
		ON CONFLICT(symbol_id) DO UPDATE SET ts=excluded.ts, label=excluded.label, strength=excluded.strength, note=excluded.note`,
		symbolID, ts, label, strength, note); err != nil {
		return err
	}
	if prev != "" && prev != label {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO regime_changes (symbol_id, ts, from_lbl, to_lbl) VALUES (?,?,?,?)`,
			symbolID, ts, prev, label); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RegimeState is a stored regime snapshot.
type RegimeState struct {
	Symbol   string  `json:"symbol"`
	Market   string  `json:"market"`
	Ts       int64   `json:"ts"`
	Label    string  `json:"label"`
	Strength float64 `json:"strength"`
	Note     string  `json:"note"`
}

// Regimes returns the latest regime for every symbol.
func (s *Store) Regimes(ctx context.Context) ([]RegimeState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, sym.market, r.ts, r.label, r.strength, r.note
		FROM regime_state r JOIN symbols sym ON sym.id=r.symbol_id
		ORDER BY sym.market, sym.symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeState
	for rows.Next() {
		var r RegimeState
		if err := rows.Scan(&r.Symbol, &r.Market, &r.Ts, &r.Label, &r.Strength, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegimeChange is one logged transition.
type RegimeChange struct {
	Symbol string `json:"symbol"`
	Ts     int64  `json:"ts"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// RecentRegimeChanges returns the newest transitions across all symbols.
func (s *Store) RecentRegimeChanges(ctx context.Context, limit int) ([]RegimeChange, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, c.ts, c.from_lbl, c.to_lbl
		FROM regime_changes c JOIN symbols sym ON sym.id=c.symbol_id
		ORDER BY c.ts DESC, c.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeChange
	for rows.Next() {
		var c RegimeChange
		if err := rows.Scan(&c.Symbol, &c.Ts, &c.From, &c.To); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── rankings ────────────────────────────────────────────────────────────

// Ranked is one row of a ranking snapshot.
type Ranked struct {
	Symbol string  `json:"symbol"`
	Market string  `json:"market"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank"`
	Ret1M  float64 `json:"ret1m"`
	Ret3M  float64 `json:"ret3m"`
}

// ReplaceRanking swaps in a full ranking snapshot for timestamp ts.
func (s *Store) ReplaceRanking(ctx context.Context, ts int64, rows []struct {
	SymbolID     int64
	Score        float64
	Rank         int
	Ret1M, Ret3M float64
}) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM rankings WHERE ts=?`, ts); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rankings (ts, symbol_id, score, rank, ret1m, ret3m) VALUES (?,?,?,?,?,?)`,
			ts, r.SymbolID, r.Score, r.Rank, r.Ret1M, r.Ret3M); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestRanking returns the most recent ranking snapshot.
func (s *Store) LatestRanking(ctx context.Context) ([]Ranked, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, sym.market, r.score, r.rank, r.ret1m, r.ret3m
		FROM rankings r JOIN symbols sym ON sym.id=r.symbol_id
		WHERE r.ts=(SELECT MAX(ts) FROM rankings) ORDER BY r.rank`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []Ranked
	for rows.Next() {
		var r Ranked
		if err := rows.Scan(&r.Symbol, &r.Market, &r.Score, &r.Rank, &r.Ret1M, &r.Ret3M); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── breakouts ───────────────────────────────────────────────────────────

// InsertBreakout records a detected event (dedup handled by the caller).
func (s *Store) InsertBreakout(ctx context.Context, symbolID *int64, ts int64, kind, detail string, strength float64) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO breakouts (symbol_id, ts, kind, detail, strength) VALUES (?,?,?,?,?)`,
		symbolID, ts, kind, detail, strength)
	return err
}

// Breakout is a stored detection.
type Breakout struct {
	Symbol   string  `json:"symbol"`
	Ts       int64   `json:"ts"`
	Kind     string  `json:"kind"`
	Detail   string  `json:"detail"`
	Strength float64 `json:"strength"`
}

// RecentBreakouts returns the newest detections.
func (s *Store) RecentBreakouts(ctx context.Context, limit int) ([]Breakout, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(sym.symbol,''), b.ts, b.kind, b.detail, b.strength
		FROM breakouts b LEFT JOIN symbols sym ON sym.id=b.symbol_id
		ORDER BY b.ts DESC, b.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []Breakout
	for rows.Next() {
		var b Breakout
		if err := rows.Scan(&b.Symbol, &b.Ts, &b.Kind, &b.Detail, &b.Strength); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LastBreakoutTs returns the newest breakout time for a symbol+kind (dedup).
func (s *Store) LastBreakoutTs(ctx context.Context, symbolID int64, kind string) (int64, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(ts) FROM breakouts WHERE symbol_id=? AND kind=?`, symbolID, kind).Scan(&ts)
	return ts.Int64, err
}

// LegValuesBySymbol returns ONE SYMBOL's stored values for a single ensemble
// leg, read out of the frozen predictions.components snapshot. Newest first,
// one row per (symbol, TRADING day) via trading_day() — the runner re-scores
// each symbol ~138 times a day and every one of those rows carries the same
// day's leg reading, so grading them as independent is the pseudo-replication
// that has cost this project twice.
//
// UNLABELED, deliberately. It does NOT join prediction_outcomes, for two
// reasons. First, an EVIDENCE row (n_used=0, no leg admitted) has no outcome
// row by design, and those are exactly the symbol-days a benched leg produces —
// joining outcomes would narrow a leg's grade to the symbols that still emit,
// which is the leg grading itself only where it already won. Second, the frozen
// labels and a recomputation from today's bars are different vintages: measured
// over 3,000 resolved rows they agree on the SIGN 99.80% of the time at 1w but
// only 94.17% at 1d (median drift 9bps, the shape of a daily bar that was still
// forming when the label was frozen). Mixing vintages inside one AUC would put
// two different label definitions in one estimate. The caller labels every row
// itself, from final bars, by the resolver's own rule.
//
// legKey is a components JSON key (e.g. "ExpectancyHitRate"). A row whose key is
// absent or non-numeric is skipped rather than defaulted — an absent leg value
// is not a zero.
func (s *Store) LegValuesBySymbol(ctx context.Context, symbolID int64, h md.Horizon, legKey string, limit int) (tss []int64, vals []float64, err error) {
	rows, qerr := s.db.QueryContext(ctx, `
		SELECT ts, val FROM (
			SELECT p.ts AS ts, json_extract(p.components, '$.'||?) AS val,
			       ROW_NUMBER() OVER (
			         PARTITION BY trading_day(p.ts)
			         ORDER BY p.ts DESC
			       ) AS rn
			FROM predictions p
			WHERE p.symbol_id=? AND p.horizon=?
		)
		WHERE rn=1 AND val IS NOT NULL
		ORDER BY ts DESC LIMIT ?`,
		legKey, symbolID, string(h), limit)
	if qerr != nil {
		return nil, nil, qerr
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var ts int64
		var v sql.NullFloat64
		if err := rows.Scan(&ts, &v); err != nil {
			return nil, nil, err
		}
		if !v.Valid {
			continue
		}
		tss = append(tss, ts)
		vals = append(vals, v.Float64)
	}
	return tss, vals, rows.Err()
}

// EvidenceDayBySymbol returns, per symbol, the newest TRADING DAY on which an
// evidence row (n_used=0 — no leg admitted, see UpsertPrediction) was recorded
// for one horizon.
//
// The runner uses it to write at most ONE evidence row per symbol per trading
// day. Every consumer of these rows dedups to one row per (symbol, trading day)
// anyway — that is the independence unit this whole project grades on — so the
// other ~137 passes a day would add nothing but rows. Measured on the live
// table that is ~40,200 rows a day at 1d against a predictions table holding
// 363,355 in total, into a database already sitting at its storage floor.
//
// One query per horizon per pass, not one per symbol: the same
// no-N+1 rule the rest of PredictionRunner.Run follows.
func (s *Store) EvidenceDayBySymbol(ctx context.Context, h md.Horizon) (map[int64]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, MAX(trading_day(ts)) FROM predictions
		WHERE horizon=? AND n_used=0 GROUP BY symbol_id`, string(h))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]int64{}
	for rows.Next() {
		var id, day int64
		if err := rows.Scan(&id, &day); err != nil {
			return nil, err
		}
		out[id] = day
	}
	return out, rows.Err()
}
