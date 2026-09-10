package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNullNotFrozen refuses a forecast that arrives without both nulls.
//
// A null computed after the outcome is known is hindsight, and hindsight is
// how a model comes to look better than it was. regime_outcomes enforces the
// same rule for naive_label; this is that rule for this predictor, at the
// write path rather than in a reviewer's memory.
var ErrNullNotFrozen = errors.New("rv_forecasts: both nulls must be frozen at call time")

// RVForecast is one frozen forecast plus the nulls it will be graded against.
type RVForecast struct {
	SymbolID                   int64
	Ts                         int64
	Horizon                    int
	RVHat                      float64
	NullRW, NullEWMA           float64
	Beta0, BetaD, BetaW, BetaM float64
	ResidVar                   float64
	NTrain                     int
	Revision                   string
}

// FreezeRVForecast freezes a forecast ONCE and reports whether this call wrote
// the row; false means a row for (symbol, ts, horizon) already existed - open,
// resolved or ungradable - and nothing about it was touched. Frozen means
// frozen at FIRST write (2026-09-09). Until this change the upsert refreshed an
// unresolved row on every pass, so a "frozen" forecast could be rewritten by
// any later pass until it resolved: on 2026-09-08 a pass at 09:36 ET froze 281
// rows from the session's forming bar and a pass after the close silently
// replaced them; the record survived by luck of the later pass. The runner now
// refuses a call bar that is not the last completed session, so the first write
// is the settled one, and the store makes it the only one. A revised bar after
// the freeze changes the OUTCOME the resolver computes, never the forecast that
// was made.
func (s *Store) FreezeRVForecast(ctx context.Context, f RVForecast, now time.Time) (bool, error) {
	if f.NullRW <= 0 || f.NullEWMA <= 0 {
		return false, ErrNullNotFrozen
	}
	if f.RVHat <= 0 || f.Horizon < 1 || f.Revision == "" {
		return false, errors.New("rv_forecasts: refusing an incomplete forecast row")
	}
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO rv_forecasts
		  (symbol_id, ts, horizon, rv_hat, null_rw, null_ewma,
		   beta0, beta_d, beta_w, beta_m, resid_var, n_train, revision, created_ts)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id, ts, horizon) DO NOTHING`,
		f.SymbolID, f.Ts, f.Horizon, f.RVHat, f.NullRW, f.NullEWMA,
		f.Beta0, f.BetaD, f.BetaW, f.BetaM, f.ResidVar, f.NTrain, f.Revision, now.Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UpsertRVForecast is FreezeRVForecast for callers that only need idempotency:
// re-running the worker in the same session must not create a second row.
func (s *Store) UpsertRVForecast(ctx context.Context, f RVForecast, now time.Time) error {
	_, err := s.FreezeRVForecast(ctx, f, now)
	return err
}

// ResolveRVForecast records the realised outcome for a frozen forecast.
func (s *Store) ResolveRVForecast(ctx context.Context, symbolID, ts int64, horizon int, actual float64, now time.Time) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE rv_forecasts SET actual=?, resolved_ts=?
		 WHERE symbol_id=? AND ts=? AND horizon=?
		   AND actual IS NULL AND ungradable IS NULL`,
		actual, now.Unix(), symbolID, ts, horizon)
	return err
}

// MarkRVUngradable closes a forecast that can never resolve, with a STATED
// reason. Abandoning it silently would leave the open-forecast index growing
// forever and would quietly drop the unfavourable cases -- a forecast is most
// likely to be unresolvable exactly when its symbol's data went bad.
func (s *Store) MarkRVUngradable(ctx context.Context, symbolID, ts int64, horizon int, reason string, now time.Time) error {
	if reason == "" {
		return errors.New("rv_forecasts: ungradable requires a reason")
	}
	_, err := s.w.ExecContext(ctx, `
		UPDATE rv_forecasts SET ungradable=?, resolved_ts=?
		 WHERE symbol_id=? AND ts=? AND horizon=? AND actual IS NULL AND ungradable IS NULL`,
		reason, now.Unix(), symbolID, ts, horizon)
	return err
}

// OpenRVForecasts lists frozen forecasts whose window has closed and which
// still need an outcome.
func (s *Store) OpenRVForecasts(ctx context.Context, horizon int, limit int) ([]RVForecast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, horizon, rv_hat, null_rw, null_ewma,
		       beta0, beta_d, beta_w, beta_m, resid_var, n_train, revision
		  FROM rv_forecasts
		 WHERE horizon=? AND actual IS NULL AND ungradable IS NULL
		 ORDER BY ts LIMIT ?`, horizon, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RVForecast
	for rows.Next() {
		var f RVForecast
		if err := rows.Scan(&f.SymbolID, &f.Ts, &f.Horizon, &f.RVHat, &f.NullRW, &f.NullEWMA,
			&f.Beta0, &f.BetaD, &f.BetaW, &f.BetaM, &f.ResidVar, &f.NTrain, &f.Revision); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RVRecord is the graded record for one horizon: what is used to say anything
// about live performance.
type RVRecord struct {
	Horizon      int
	N            int
	DistinctDays int
	MeanQLIKEHAR float64
	MeanQLIKERW  float64
	MeanQLIKEEW  float64
	Ungradable   int
}

// RVLiveRecord aggregates resolved forecasts.
//
// It reports DistinctDays beside N because they are not the same evidence.
// Forecasts resolving on one day share a market shock, so the day count is the
// number of independent observations and N is not. Returning only N would
// invite exactly the inflation this platform has already retired predictors
// for.
func (s *Store) RVLiveRecord(ctx context.Context, horizon int) (RVRecord, error) {
	r := RVRecord{Horizon: horizon}
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(DISTINCT date(ts,'unixepoch')),
		       COALESCE(AVG(actual/rv_hat    - LN(actual/rv_hat)    - 1), 0),
		       COALESCE(AVG(actual/null_rw   - LN(actual/null_rw)   - 1), 0),
		       COALESCE(AVG(actual/null_ewma - LN(actual/null_ewma) - 1), 0)
		  FROM rv_forecasts
		 WHERE horizon=? AND actual IS NOT NULL AND actual > 0`, horizon).
		Scan(&r.N, &r.DistinctDays, &r.MeanQLIKEHAR, &r.MeanQLIKERW, &r.MeanQLIKEEW)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rv_forecasts WHERE horizon=? AND ungradable IS NOT NULL`,
		horizon).Scan(&r.Ungradable); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	return r, nil
}

// CountResolvedRV is the pre-flight the registrar uses: a forward test filed
// after its own results are readable is not a registration.
func (s *Store) CountResolvedRV(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rv_forecasts WHERE actual IS NOT NULL`).Scan(&n)
	return n, err
}