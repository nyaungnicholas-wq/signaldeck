package store

import (
	"context"
	"database/sql"
	"errors"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

type PaperAbstention struct {
	SinceTs        int64   `json:"sinceTs"`
	Forecasts      int     `json:"forecasts"`
	AboveLong      int     `json:"aboveLong"`
	MaxCalProb     float64 `json:"maxCalProb"`
	Decisions      int     `json:"decisions"`
	DoNothing      int     `json:"doNothing"`
	LastDecisionTs int64   `json:"lastDecisionTs"`
}

func (s *Store) PaperAbstentionStats(ctx context.Context, strategy string, h md.Horizon, longThresh float64, sinceTs int64) (PaperAbstention, error) {
	var pa PaperAbstention
	pa.SinceTs = sinceTs

	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN cal_prob >= ? THEN 1 ELSE 0 END),0), COALESCE(MAX(cal_prob),0)
		FROM predictions
		WHERE horizon = ? AND n_used > 0 AND ts >= ?
	`, longThresh, string(h), sinceTs).Scan(&pa.Forecasts, &pa.AboveLong, &pa.MaxCalProb)
	if err != nil {
		return pa, err
	}

	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN decision='DO_NOTHING' THEN 1 ELSE 0 END),0)
		FROM ev_decisions
		WHERE strategy = ? AND ts >= ?
	`, strategy, sinceTs).Scan(&pa.Decisions, &pa.DoNothing)
	if err != nil {
		return pa, err
	}

	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(ts),0)
		FROM ev_decisions
		WHERE strategy = ?
	`, strategy).Scan(&pa.LastDecisionTs)
	if err != nil {
		return pa, err
	}

	return pa, nil
}

type WorkerLastRun struct {
	Worker     string `json:"worker"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	Revision   string `json:"revision"`
}

func (s *Store) LastWorkerRun(ctx context.Context, worker string) (WorkerLastRun, bool, error) {
	var w WorkerLastRun
	err := s.db.QueryRowContext(ctx, `
		SELECT worker, started_at, COALESCE(finished_at,0), status, COALESCE(detail,''), COALESCE(revision,'')
		FROM worker_runs
		WHERE worker = ?
		ORDER BY id DESC
		LIMIT 1
	`, worker).Scan(&w.Worker, &w.StartedAt, &w.FinishedAt, &w.Status, &w.Detail, &w.Revision)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return w, false, nil
		}
		return w, false, err
	}
	return w, true, nil
}
