// Feature-distribution sampling for drift detection (2026-07-25).
//
// The model-health gate scores a "stability" axis from the fraction of features
// whose distribution has moved away from what the model learned on. Nothing
// computed that fraction, so the axis was inert. This supplies the two samples
// it needs: a REFERENCE window (older, what the model was fit against) and a
// LIVE window (recent, what it is being asked to predict from).
//
// The split is by TIME rather than random, because that is the drift that
// matters: a random split would compare a model to itself and always look
// stable.
package store

import (
	"context"
	"encoding/json"
)

// FeatureWindows returns per-feature value slices for a reference period and a
// live period, keyed by feature name. Vectors are stored as JSON objects, and
// only the requested featureVersion is read — comparing across versions would
// measure a schema change and call it drift.
func (s *Store) FeatureWindows(ctx context.Context, version int,
	refFrom, refTo, liveFrom, liveTo int64, limit int,
) (reference, live map[string][]float64, err error) {
	reference = map[string][]float64{}
	live = map[string][]float64{}
	if limit <= 0 {
		limit = 20000
	}

	load := func(from, to int64, into map[string][]float64) error {
		rows, qerr := s.db.QueryContext(ctx, `
			SELECT vec FROM features
			WHERE version = ? AND ts >= ? AND ts < ?
			ORDER BY ts DESC LIMIT ?`, version, from, to, limit)
		if qerr != nil {
			return qerr
		}
		defer rows.Close() //nolint:errcheck
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var vec map[string]float64
			if json.Unmarshal([]byte(raw), &vec) != nil {
				continue // a malformed row is skipped, never guessed at
			}
			for k, v := range vec {
				into[k] = append(into[k], v)
			}
		}
		return rows.Err()
	}

	if err = load(refFrom, refTo, reference); err != nil {
		return nil, nil, err
	}
	if err = load(liveFrom, liveTo, live); err != nil {
		return nil, nil, err
	}
	return reference, live, nil
}

// LatestFeatureVersion reports the newest feature-vector version present, so
// drift is always measured within one schema rather than across a migration.
func (s *Store) LatestFeatureVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM features`).Scan(&v)
	return v, err
}
