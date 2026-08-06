// caldiag answers ONE question: why did the fleet-wide calibration publish, or
// refuse to publish, for a horizon today?
//
// The daemon logs "refusing a rank-collapsing map" and nothing else, which is
// true but not actionable — it does not say whether the rank-preserving fit LOST
// a held-out comparison or never ran at all. Those look identical in the log and
// call for opposite responses. On 2026-08-05 it was the second: fitBeta's
// undamped Newton step ran to infinity on near-separable data, so the comparison
// the design depends on had never executed. Finding that took a day; this makes
// it a one-command check.
//
// Reads the database mode=ro, so it can never migrate or write the live file.
//
//	go run ./cmd/caldiag -h 1d
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"

	_ "modernc.org/sqlite"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

func main() {
	db := flag.String("db", `C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data\signaldeck.db`, "database path")
	horizon := flag.String("h", "1d", "horizon")
	limit := flag.Int("limit", 40000, "pair limit (mirrors pipeline.calibrationPairLimit)")
	flag.Parse()

	conn, err := sql.Open("sqlite", "file:"+*db+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer conn.Close() //nolint:errcheck

	raws, ups, days, err := pairs(conn, *horizon, *limit)
	if err != nil {
		fmt.Println("query:", err)
		os.Exit(1)
	}
	if len(raws) == 0 {
		fmt.Printf("horizon=%s: no resolved pairs\n", *horizon)
		return
	}

	// Same construction as pipeline.globalCalibration: oldest first, Ts = UTC day.
	ps := make([]ensemble.Pair, len(raws))
	for i := range raws {
		src := len(raws) - 1 - i
		ps[i] = ensemble.Pair{Pred: raws[src], Actual: ups[src], Ts: days[src]}
	}

	lo, hi, base := math.Inf(1), math.Inf(-1), 0.0
	for _, p := range ps {
		lo, hi, base = math.Min(lo, p.Pred), math.Max(hi, p.Pred), base+p.Actual
	}
	fmt.Printf("horizon=%s  rows=%d  distinct UTC days=%d (floor 10)\n",
		*horizon, len(ps), countDays(ps))
	fmt.Printf("raw range=[%.4f,%.4f]  base rate=%.4f\n", lo, hi, base/float64(len(ps)))
	if lo <= 0 || hi >= 1 {
		fmt.Println("  note: raw reaches the closed interval — the beta likelihood is near-unbounded here,")
		fmt.Println("        which is exactly the case an undamped Newton step used to diverge on")
	}

	// Reproduce the holdout split, including the Ts-boundary snap.
	ordered := append([]ensemble.Pair(nil), ps...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Ts < ordered[j].Ts })
	cut := len(ordered) * 3 / 4
	for cut < len(ordered) && ordered[cut].Ts == ordered[cut-1].Ts {
		cut++
	}
	train, test := ordered[:cut], ordered[cut:]
	fmt.Printf("split: train=%d rows / %d days   holdout=%d rows / %d days\n",
		len(train), countDays(train), len(test), countDays(test))

	fn, calibrated, ranked := ensemble.CalibrateRanking(ps)
	fmt.Printf("\nCalibrateRanking -> calibrated=%v ranked=%v\n", calibrated, ranked)
	switch {
	case !calibrated:
		fmt.Println("VERDICT: no map fitted — the pipeline publishes raw, uncorrected.")
	case !ranked:
		fmt.Println("VERDICT: isotonic won the held-out comparison, so the map would collapse the")
		fmt.Println("         ranking. The pipeline REFUSES it and publishes raw, uncorrected.")
	default:
		fmt.Println("VERDICT: a rank-preserving map shipped.")
	}

	// How many distinct values does each candidate emit? This is the property the
	// guard exists to protect, and the number that makes the tradeoff concrete.
	if iso, ok := ensemble.Calibrate(train); ok && len(test) > 0 {
		fmt.Printf("distinct outputs over holdout: isotonic=%d  shipped=%d\n",
			distinctOut(iso, test), distinctOut(fn, test))
	}
}

func distinctOut(f func(float64) float64, ps []ensemble.Pair) int {
	s := map[float64]bool{}
	for _, p := range ps {
		s[math.Round(f(p.Pred)*10000)/10000] = true
	}
	return len(s)
}

func countDays(ps []ensemble.Pair) int {
	s := map[int64]bool{}
	for _, p := range ps {
		s[p.Ts] = true
	}
	return len(s)
}

// pairs mirrors store.ResolvedRawPredictionPairs.
func pairs(db *sql.DB, h string, limit int) (raws, ups []float64, days []int64, err error) {
	rows, err := db.QueryContext(context.Background(), `
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
		ORDER BY day DESC LIMIT ?`, h, limit)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var p float64
		var u int
		var d int64
		if err := rows.Scan(&p, &u, &d); err != nil {
			return nil, nil, nil, err
		}
		raws, ups, days = append(raws, p), append(ups, float64(u)), append(days, d)
	}
	return raws, ups, days, rows.Err()
}
