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

// UpsertRVForecast freezes a forecast. Idempotent on (symbol, ts, horizon):
// re-running the worker in the same session must not create a second row, and
// must not overwrite a row that has already RESOLVED -- a resolved outcome is
// evidence, and silently replacing it would be rewriting the record.
func (s *Store) UpsertRVForecast(ctx context.Context, f RVForecast, now time.Time) error {
	if f.NullRW <= 0 || f.NullEWMA <= 0 {
		return ErrNullNotFrozen
	}
	if f.RVHat <= 0 || f.Horizon < 1 || f.Revision == "" {
		return errors.New("rv_forecasts: refusing an incomplete forecast row")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rv_forecasts
		  (symbol_id, ts, horizon, rv_hat, null_rw, null_ewma,
		   beta0, beta_d, beta_w, beta_m, resid_var, n_train, revision, created_ts)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id, ts, horizon) DO UPDATE SET
		  rv_hat=excluded.rv_hat, null_rw=excluded.null_rw, null_ewma=excluded.null_ewma,
		  beta0=excluded.beta0, beta_d=excluded.beta_d, beta_w=excluded.beta_w,
		  beta_m=excluded.beta_m, resid_var=excluded.resid_var,
		  n_train=excluded.n_train, revision=excluded.revision
		WHERE rv_forecasts.actual IS NULL AND rv_forecasts.ungradable IS NULL`,
		f.SymbolID, f.Ts, f.Horizon, f.RVHat, f.NullRW, f.NullEWMA,
		f.Beta0, f.BetaD, f.BetaW, f.BetaM, f.ResidVar, f.NTrain, f.Revision, now.Unix())
	return err
}

// ResolveRVForecast records the realised outcome for a frozen forecast.
func (s *Store) ResolveRVForecast(ctx context.Context, symbolID, ts int64, horizon int, actual float64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
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
	_, err := s.db.ExecContext(ctx, `
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
