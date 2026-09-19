// Command rvcrosscheck proves that daemon/internal/harrv and
// tools/rv_forecast_backtest.py compute the SAME realized-variance series.
//
// The two are deliberately separate implementations of one specification: the
// Go one runs live, the Python one produces the backtest. Two implementations
// that agree are the cheapest defence against the failure mode that killed the
// earlier predictors in this repository -- a transcription error that produces
// plausible numbers nobody can see is wrong.
//
// Measured on SPY, 2018-2026: 1,928 values compared, 0 mismatches, maximum
// relative difference 9.4e-16, which is machine precision.
//
// Usage:
//
//	python tools/rv_forecast_backtest.py --crosscheck SPY
//	go run -C daemon ./cmd/rvcrosscheck ../data/signaldeck.db SPY ../scratchpad/rv_SPY.csv
//
// Exits non-zero on any disagreement above 1e-9, so it can gate a release.
package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/nyaungnicholas-wq/signaldeck/internal/harrv"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: rvcrosscheck <db> <symbol> <python-csv>")
		os.Exit(2)
	}
	dbPath, sym, csvPath := os.Args[1], os.Args[2], os.Args[3]
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		panic(err)
	}
	defer db.Close() //nolint:errcheck

	var id int64
	if err := db.QueryRow("SELECT id FROM symbols WHERE symbol=? LIMIT 1", sym).Scan(&id); err != nil {
		panic(err)
	}
	rows, err := db.Query("SELECT ts,open,high,low,close FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts", id)
	if err != nil {
		panic(err)
	}
	var bars []md.Bar
	for rows.Next() {
		var b md.Bar
		if err := rows.Scan(&b.Ts, &b.Open, &b.High, &b.Low, &b.Close); err != nil {
			panic(err)
		}
		bars = append(bars, b)
	}
	rows.Close() //nolint:errcheck

	rv, ts, x := harrv.RVSeries(bars)
	goVals := map[int64]float64{}
	for i := range rv {
		if !math.IsNaN(rv[i]) {
			goVals[ts[i]] = rv[i]
		}
	}
	fmt.Printf("go:  %d bars, %d estimable, exclusions %+v\n", len(bars), len(goVals), x)

	f, err := os.Open(csvPath)
	if err != nil {
		panic(err)
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	var n, mismatch int
	var maxRel float64
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ",")
		if len(parts) != 2 {
			continue
		}
		t, _ := strconv.ParseInt(parts[0], 10, 64)
		py, _ := strconv.ParseFloat(parts[1], 64)
		g, ok := goVals[t]
		if !ok {
			fmt.Printf("MISSING in go: ts=%d\n", t)
			mismatch++
			continue
		}
		n++
		rel := math.Abs(g-py) / math.Max(math.Abs(py), 1e-300)
		if rel > maxRel {
			maxRel = rel
		}
		if rel > 1e-9 {
			mismatch++
			if mismatch < 5 {
				fmt.Printf("DIFF ts=%d go=%.17g py=%.17g rel=%g\n", t, g, py, rel)
			}
		}
	}
	fmt.Printf("compared %d values, %d mismatches, max relative diff %g\n", n, mismatch, maxRel)
	if mismatch == 0 && n > 1000 {
		fmt.Println("CROSSCHECK OK - the Go and Python estimators agree to 1e-9")
		return
	}
	os.Exit(1)
}
