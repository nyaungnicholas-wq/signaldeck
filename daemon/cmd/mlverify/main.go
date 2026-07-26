package main

// Read-only verification of internal/metalabel against the LIVE SignalDeck DB.
// Opens the database in mode=ro so the running daemon is never disturbed.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/metalabel"
	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:/Users/natalienyaung/claude code/signaldeck/data/signaldeck.db?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// One INDEPENDENT observation per (symbol, UTC-day): pooling intraday rows
	// inflates n ~60x on this platform and has manufactured fake significance here.
	// Labels come from prediction_outcomes (resolved only), joined exactly the
	// way store.LabeledFeatures does it. Deduped to ONE row per (symbol, UTC-day):
	// pooling intraday rows inflates n ~60x here and has faked significance before.
	rows, err := db.Query(`
		SELECT f.symbol_id, f.ts, f.vec, o.fwd_return
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		WHERE f.horizon='1d' AND o.resolved_at IS NOT NULL
		  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL
		  AND f.ts = (SELECT MAX(g.ts) FROM features g
		              JOIN prediction_outcomes go2
		                ON go2.symbol_id=g.symbol_id AND go2.horizon=g.horizon AND go2.ts=g.ts
		              WHERE g.symbol_id=f.symbol_id AND g.horizon='1d'
		                AND g.ts/86400 = f.ts/86400
		                AND go2.resolved_at IS NOT NULL AND go2.fwd_return IS NOT NULL)
		ORDER BY f.ts ASC
		LIMIT 40000`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	type raw struct {
		ts  int64
		vec map[string]float64
		fwd float64
	}
	var all []raw
	for rows.Next() {
		var sid, ts int64
		var vecs string
		var fwd sql.NullFloat64
		if err := rows.Scan(&sid, &ts, &vecs, &fwd); err != nil {
			continue
		}
		if !fwd.Valid {
			continue
		}
		var v map[string]float64
		if json.Unmarshal([]byte(vecs), &v) != nil {
			continue
		}
		all = append(all, raw{ts: ts, vec: v, fwd: fwd.Float64})
	}
	fmt.Printf("independent labeled rows loaded: %d\n", len(all))
	if len(all) < 200 {
		fmt.Println("not enough data to grade")
		return
	}

	// Primary = the platform's own calibrated directional call (the retired one).
	// Context = circumstance features ONLY, never a restatement of the call.
	ctxKeys := []string{"vix_level", "vix_regime", "comp_vol_regime", "rank_pct",
		"regime_squeeze", "adx14", "trend_class", "bb_pctb", "n_used"}

	var samples []metalabel.Sample
	for _, r := range all {
		p, ok := r.vec["pred_cal"]
		if !ok {
			if p, ok = r.vec["pred_raw"]; !ok {
				continue
			}
		}
		ctx := make([]float64, len(ctxKeys))
		present := 0
		for i, k := range ctxKeys {
			if v, ok := r.vec[k]; ok {
				ctx[i] = v
				present++
			}
		}
		if present < 3 {
			continue
		}
		samples = append(samples, metalabel.Sample{
			Ts: r.ts, PrimaryProb: p, Context: ctx, FwdReturn: r.fwd,
		})
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Ts < samples[j].Ts })
	fmt.Printf("directional candidates with context: %d\n\n", len(samples))

	g, err := metalabel.Evaluate(samples, 4, 0.001, metalabel.DefaultThreshold)
	if err != nil {
		fmt.Println("Evaluate error:", err)
		return
	}
	b, _ := json.MarshalIndent(g, "", "  ")
	fmt.Println(string(b))
}
