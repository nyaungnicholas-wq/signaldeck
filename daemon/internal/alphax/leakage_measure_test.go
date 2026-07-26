package alphax_test

// A7 · MEASURE THE CONSEQUENCE of excluding the leaked model outputs.
//
// The exclusion list grew from five keys to a class rule, and the honest
// question is not "does the code compile" but "how much of the pooled engine's
// out-of-sample lift was the model copying another leg?". A lift that FALLS
// when forecast_prob / forecast_lift / expectancy_hit_rate / n_used are removed
// is not a regression to be tuned back — it is the finding, and the prior
// number was partly a leg grading itself through a second leg.
//
// Opt-in because it reads the live 2 GB database and takes minutes:
//
//	SIGNALDECK_DB=/abs/path/data/signaldeck.db go test ./internal/alphax/ \
//	  -run TestA7_LeakedFeatureConsequence -v -timeout 60m
//
// The DB is opened READ-ONLY (mode=ro). The control arm does NOT re-enable the
// exclusion: it RENAMES the four keys to leak_* so identical values survive the
// filter. That keeps the measurement honest about what is being compared — the
// same rows, the same purged day-blocked walk-forward, four extra columns —
// and makes it impossible for this test to reintroduce the leak in production.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/alphax"
	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
	_ "modernc.org/sqlite"
)

// leakedKeys are the four A7 keys, in the spelling they carry in the store.
var leakedKeys = []string{"forecast_prob", "forecast_lift", "expectancy_hit_rate", "n_used"}

func TestA7_LeakedFeatureConsequence(t *testing.T) {
	path := os.Getenv("SIGNALDECK_DB")
	if path == "" {
		t.Skip("set SIGNALDECK_DB to the live database path to measure A7's consequence")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck

	// Mirrors AlphaXTrainer.Run: pooled, v3+, newest-first, capped at 50k.
	for _, h := range []string{"1d", "1w"} {
		rows := loadPooled(t, db, h)
		if len(rows) == 0 {
			t.Logf("%s: no resolved labeled rows", h)
			continue
		}
		present := map[string]int{}
		for _, r := range rows {
			for _, k := range leakedKeys {
				if _, ok := r.Features[k]; ok {
					present[k]++
				}
			}
		}
		t.Logf("%s: %d pooled rows; leaked-key coverage %v", h, len(rows), present)

		embargo := 2
		if h == "1w" {
			embargo = 8
		}
		clean, okC, whyC := alphax.Evaluate(alphax.BuildDataset(rows), 5, embargo, gbm.Defaults())
		leaky, okL, whyL := alphax.Evaluate(alphax.BuildDataset(withLeaks(rows)), 5, embargo, gbm.Defaults())
		if !okC || !okL {
			t.Logf("%s: refused to grade (clean=%q leaky=%q)", h, whyC, whyL)
			continue
		}
		t.Logf("%s CLEAN (A7 fix): n=%d acc=%.4f base=%.4f lift=%+.4f auc=%.4f brier=%.4f",
			h, clean.N, clean.Accuracy, clean.BaseRate, clean.Lift, clean.AUC, clean.BrierScore)
		t.Logf("%s LEAKY (pre-fix): n=%d acc=%.4f base=%.4f lift=%+.4f auc=%.4f brier=%.4f",
			h, leaky.N, leaky.Accuracy, leaky.BaseRate, leaky.Lift, leaky.AUC, leaky.BrierScore)
		t.Logf("%s DELTA lift %+.4f, auc %+.4f (negative = the removed lift was the copy)",
			h, clean.Lift-leaky.Lift, clean.AUC-leaky.AUC)
	}
}

// withLeaks re-admits the four excluded keys under leak_-prefixed names, so the
// control arm trains on their values without any production predicate changing.
func withLeaks(rows []alphax.LabeledRow) []alphax.LabeledRow {
	out := make([]alphax.LabeledRow, len(rows))
	for i, r := range rows {
		f := make(map[string]float64, len(r.Features)+len(leakedKeys))
		for k, v := range r.Features {
			f[k] = v
		}
		for _, k := range leakedKeys {
			if v, ok := r.Features[k]; ok {
				f["leak_"+k] = v
			}
		}
		out[i] = alphax.LabeledRow{SymbolID: r.SymbolID, Ts: r.Ts, Features: f, FwdReturn: r.FwdReturn}
	}
	return out
}

func loadPooled(t *testing.T, db *sql.DB, horizon string) []alphax.LabeledRow {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT f.symbol_id, f.ts, f.vec, o.fwd_return
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		WHERE f.horizon=? AND f.version>=3 AND o.resolved_at IS NOT NULL
		  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL
		ORDER BY f.ts DESC LIMIT 50000`, horizon)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck
	var out []alphax.LabeledRow
	for rows.Next() {
		var r alphax.LabeledRow
		var vec string
		if err := rows.Scan(&r.SymbolID, &r.Ts, &vec, &r.FwdReturn); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(vec), &r.Features); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
