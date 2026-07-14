package store

import (
	"context"
	"testing"
)

// L2 (adversarial review) — LabeledFeaturesAll filters features by horizon
// and orders by ts DESC every 6h trainer pass; the only prior index led with
// symbol_id, forcing a full scan + sort. The schema must carry an index that
// serves that access path.
func TestSchema_FeaturesHorizonTsIndexExists(t *testing.T) {
	st := openTemp(t)
	var n int
	if err := st.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='index' AND name='idx_features_h_ts' AND tbl_name='features'`).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("idx_features_h_ts missing: LabeledFeaturesAll would full-scan features every 6h pass")
	}
}
