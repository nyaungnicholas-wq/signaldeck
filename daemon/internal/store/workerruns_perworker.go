package store

import (
	"context"
	"database/sql"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// RecentWorkerRunsPerWorker returns up to perWorker most-recent runs for every
// worker that has any run at all, with no time filtering. The fleet staleness
// watchdog previously judged staleness from RecentWorkerRuns(ctx, 2000), a
// fixed row cap feeding a time-based verdict. At ~3,000 runs/day that window
// spans only 3.6 hours, so workers on daily or weekly cadences had zero rows
// in it and were never judged at all. PruneWorkerRuns already retains the
// newest 20 runs per worker, so the needed history is deliberately kept but
// was not read that way. Reading per worker removes a global row cap from a
// per-worker judgment. There is deliberately NO time floor: a since-bound
// would drop any worker whose newest run predates it — the longest-dead
// worker, exactly the case the watchdog exists to catch — so a floor would
// reproduce the defect inside its own fix. Pruning already bounds the table.
func (s *Store) RecentWorkerRunsPerWorker(ctx context.Context, perWorker int) ([]md.WorkerRun, error) {
	if perWorker <= 0 {
		// NOT sql.ErrNoRows: callers test that sentinel to mean "no data", so
		// returning it for a bad argument would let a programming error read as
		// an empty fleet — the watchdog reporting health it never measured.
		return nil, fmt.Errorf("RecentWorkerRunsPerWorker: perWorker must be positive, got %d", perWorker)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, worker, started_at, finished_at, status, detail FROM (
			SELECT id, worker, started_at, finished_at, status, detail,
			       ROW_NUMBER() OVER (PARTITION BY worker ORDER BY started_at DESC, id DESC) AS rn
			FROM worker_runs
		) WHERE rn <= ? ORDER BY worker ASC, started_at DESC, id DESC`, perWorker)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.WorkerRun
	for rows.Next() {
		var r md.WorkerRun
		var fin sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Worker, &r.StartedAt, &fin, &r.Status, &r.Detail); err != nil {
			return nil, err
		}
		if fin.Valid {
			r.FinishedAt = &fin.Int64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
