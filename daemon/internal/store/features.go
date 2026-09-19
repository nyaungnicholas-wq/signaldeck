// Feature store + dataset accounting (storage-permanence wave).
//
// The feature store persists the EXACT input vector the ensemble used at
// prediction time, keyed (symbol, horizon, ts, version). Joined against the
// later-resolved prediction_outcomes rows it is an ever-growing labeled
// training set: reproducible retraining with no lookahead and no recompute
// drift. Feature rows are NEVER pruned.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ── feature store ───────────────────────────────────────────────────────

// FeatureRow is one persisted feature vector.
type FeatureRow struct {
	SymbolID int64              `json:"-"`
	Horizon  md.Horizon         `json:"horizon"`
	Ts       int64              `json:"ts"`
	Version  int                `json:"version"`
	Vec      map[string]float64 `json:"vec"`
}

// InsertFeatures persists one feature vector (idempotent on the unique key:
// re-running the predictor in the same minute replaces the vector in place).
func (s *Store) InsertFeatures(ctx context.Context, symbolID int64, h md.Horizon, ts int64, version int, vec map[string]float64) error {
	if len(vec) == 0 {
		return fmt.Errorf("store: refusing to insert empty feature vector")
	}
	b, err := json.Marshal(vec)
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `
		INSERT INTO features (symbol_id, horizon, ts, version, vec)
		VALUES (?,?,?,?,?)
		ON CONFLICT(symbol_id, horizon, ts, version) DO UPDATE SET vec=excluded.vec`,
		symbolID, string(h), ts, version, string(b))
	return err
}

// FeaturesSince returns feature vectors for one symbol+horizon with
// ts >= sinceTs, ascending, capped at limit (0 = no cap).
func (s *Store) FeaturesSince(ctx context.Context, symbolID int64, h md.Horizon, sinceTs int64, limit int) ([]FeatureRow, error) {
	q := `SELECT ts, version, vec FROM features
	      WHERE symbol_id=? AND horizon=? AND ts>=? ORDER BY ts`
	args := []any{symbolID, string(h), sinceTs}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []FeatureRow
	for rows.Next() {
		f := FeatureRow{SymbolID: symbolID, Horizon: h}
		var vec string
		if err := rows.Scan(&f.Ts, &f.Version, &vec); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(vec), &f.Vec); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// LabeledFeature is one training example: the feature vector used at
// prediction time joined to its realized outcome.
type LabeledFeature struct {
	SymbolID  int64              `json:"-"`
	Horizon   md.Horizon         `json:"horizon"`
	Ts        int64              `json:"ts"`
	Version   int                `json:"version"`
	Vec       map[string]float64 `json:"vec"`
	Up        int                `json:"up"`        // realized 1/0
	FwdReturn float64            `json:"fwdReturn"` // realized forward return
}

// LabeledFeatures joins stored feature vectors to RESOLVED prediction
// outcomes for one horizon — the labeled training set. Newest first, capped
// at limit. Rows whose outcome was voided (resolved with NULL up/fwd_return)
// are excluded: they carry no label.
func (s *Store) LabeledFeatures(ctx context.Context, h md.Horizon, limit int) ([]LabeledFeature, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, f.ts, f.version, f.vec, o.up, o.fwd_return
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		WHERE f.horizon=? AND o.resolved_at IS NOT NULL
		  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL
		ORDER BY f.ts DESC LIMIT ?`,
		string(h), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []LabeledFeature
	for rows.Next() {
		lf := LabeledFeature{Horizon: h}
		var vec string
		if err := rows.Scan(&lf.SymbolID, &lf.Ts, &lf.Version, &vec, &lf.Up, &lf.FwdReturn); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(vec), &lf.Vec); err != nil {
			return nil, err
		}
		out = append(out, lf)
	}
	return out, rows.Err()
}

// ── cheap per-symbol lookups the feature capture uses ───────────────────

// RegimeLabels returns the latest regime label per symbol id (one query for
// the whole fleet — cheap enough to call once per predictor pass).
func (s *Store) RegimeLabels(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT symbol_id, label FROM regime_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var lbl string
		if err := rows.Scan(&id, &lbl); err != nil {
			return nil, err
		}
		out[id] = lbl
	}
	return out, rows.Err()
}

// RankingPercentiles returns the latest cross-sectional ranking percentile
// (0..100) per symbol id from the newest ranking snapshot.
func (s *Store) RankingPercentiles(ctx context.Context) (map[int64]float64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, score FROM rankings
		WHERE ts=(SELECT MAX(ts) FROM rankings)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]float64{}
	for rows.Next() {
		var id int64
		var sc float64
		if err := rows.Scan(&id, &sc); err != nil {
			return nil, err
		}
		out[id] = sc
	}
	return out, rows.Err()
}

// ── dataset accounting ──────────────────────────────────────────────────

// TableStat is one table's row count and (when it has a time column) span.
type TableStat struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
	MinTs *int64 `json:"minTs,omitempty"`
	MaxTs *int64 `json:"maxTs,omitempty"`
}

// DataStatsResult is the dataset-accounting snapshot for /api/datastats.
//
// The tiered-storage fields (ArchiveBytes + Retention) are populated by the API
// handler, which owns the archive path and retention policy — store.DataStats
// leaves them zero-valued so this package stays free of env/policy knowledge.
type DataStatsResult struct {
	Tables      []TableStat `json:"tables"`
	DBBytes     int64       `json:"dbBytes"`
	WALBytes    int64       `json:"walBytes"`
	GeneratedAt int64       `json:"generatedAt"`

	// Cold-archive size on disk (walked from the archive dir). Makes the
	// "nothing is thrown away, and it's bounded" claim visible + provable.
	ArchiveBytes int64 `json:"archiveBytes"`
	// ArchiveBytesUnknown distinguishes a MEASURED empty archive from a
	// directory walk that failed. Without it both rendered as
	// "cold archive: 0 B" -- and that string is the evidence of a
	// catastrophe, so producing it from a failed read is the worst
	// available lie on this surface.
	ArchiveBytesUnknown bool `json:"archiveBytesUnknown,omitempty"`
	// Active tiered-retention windows (human-readable), so the growth panel
	// shows exactly how long each tier stays hot before archive+prune.
	Retention *RetentionWindows `json:"retention,omitempty"`

	// Configured upper bound on the lifetime of ANY daemon read connection
	// (seconds). Published so a WAL diagnosis can state as a FACT, not a
	// hope, that no daemon reader has been open longer than N minutes — which
	// is what lets a stalled checkpoint be attributed to (or cleared of) the
	// daemon's own read pools.
	ReadConnMaxLifetimeSec int `json:"readConnMaxLifetimeSec"`
	ReadConnMaxIdleSec     int `json:"readConnMaxIdleSec"`
}

// RetentionWindows describes the active hot-store retention per tier, in the
// units the operator tunes them in. Daily bars are permanent (never pruned).
type RetentionWindows struct {
	SnapshotsHours int  `json:"snapshotsHours"` // snapshots_1s hot window (hours)
	Bars1mDays     int  `json:"bars1mDays"`     // 1m bars hot window (days) → then compact to 1h + archive
	Bars1hDays     int  `json:"bars1hDays"`     // 1h bars hot window (days) → then compact to 1d + archive
	DailyForever   bool `json:"dailyForever"`   // always true — daily is the permanent record
}

// statTables is the static (table, time-column) inventory DataStats reports
// on. Names are compile-time constants — never user input — so building the
// COUNT/MIN/MAX queries with Sprintf is safe.
var statTables = []struct{ name, tsCol string }{
	{"symbols", "added_at"},
	{"bars", "ts"},
	{"snapshots_1s", "ts"},
	{"scores", "ts"},
	{"score_outcomes", "ts"},
	{"expectancy", "updated_at"},
	{"insights", "ts"},
	{"worker_runs", "started_at"},
	{"dq_events", "ts"},
	{"forecasts", "ts"},
	{"positions", "entry_ts"},
	{"predictions", "ts"},
	{"prediction_outcomes", "ts"},
	{"regime_state", "ts"},
	{"regime_changes", "ts"},
	{"rankings", "ts"},
	{"breakouts", "ts"},
	{"news", "ts"},
	{"features", "ts"},
	// users/sessions deliberately excluded: /api/datastats is a public read
	// by default, and account/session counts are user metadata, not dataset
	// accounting.
	{"user_symbols", ""},
	{"meta", ""},
}

// DataStats returns per-table row counts and time spans plus the database
// and WAL file sizes — the raw material of the "data growth" panel that
// makes storage permanence visible and provable.
func (s *Store) DataStats(ctx context.Context) (DataStatsResult, error) {
	res := DataStatsResult{
		GeneratedAt:            time.Now().Unix(),
		ReadConnMaxLifetimeSec: int(ReadConnMaxLifetime / time.Second),
		ReadConnMaxIdleSec:     int(ReadConnMaxIdleTime / time.Second),
	}
	for _, t := range statTables {
		st := TableStat{Table: t.name}
		if t.tsCol == "" {
			if err := s.db.QueryRowContext(ctx,
				fmt.Sprintf(`SELECT COUNT(*) FROM %s`, t.name)).Scan(&st.Rows); err != nil {
				return res, fmt.Errorf("datastats %s: %w", t.name, err)
			}
		} else {
			var mn, mx sql.NullInt64
			if err := s.db.QueryRowContext(ctx,
				fmt.Sprintf(`SELECT COUNT(*), MIN(%s), MAX(%s) FROM %s`, t.tsCol, t.tsCol, t.name)).
				Scan(&st.Rows, &mn, &mx); err != nil {
				return res, fmt.Errorf("datastats %s: %w", t.name, err)
			}
			if mn.Valid {
				st.MinTs = &mn.Int64
			}
			if mx.Valid {
				st.MaxTs = &mx.Int64
			}
		}
		res.Tables = append(res.Tables, st)
	}
	if fi, err := os.Stat(s.path); err == nil {
		res.DBBytes = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		res.WALBytes = fi.Size()
	}
	return res, nil
}
