// paperreplay reconstructs the simulated paper book over a past window with the
// CURRENT code, writing to its own strategy names so the book that actually ran
// is never touched.
//
// THE OUTPUT IS A RECONSTRUCTION, NOT A TRACK RECORD. It was not accumulated in
// real time, it is computed with today's code rather than the code that ran then,
// and two inputs cannot be reconstructed at all: the advisory expectancy prior
// (its table is overwritten in place and has no history, so a replay withholds it)
// and the universe outside universe_membership's coverage (refused rather than
// approximated). Label it accordingly wherever it is shown.
//
//	go run -C daemon ./cmd/paperreplay --from 2026-07-22 --to 2026-08-19
//	go run -C daemon ./cmd/paperreplay --from 2026-07-22 --to 2026-08-19 --apply
//
// Without --apply it reports what it WOULD replay and stops, so the refusals
// (coverage gaps, a destination that already holds rows) surface before anything
// is written.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func parseDay(s string) (int64, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, fmt.Errorf("want YYYY-MM-DD: %w", err)
	}
	return t.UTC().Unix(), nil
}

func main() {
	fromS := flag.String("from", "", "first session to reconstruct, YYYY-MM-DD (required)")
	toS := flag.String("to", "", "last session to reconstruct, YYYY-MM-DD (required)")
	suffix := flag.String("suffix", "-replay", "strategy-name suffix the reconstruction is written under")
	maxAgeDays := flag.Int("max-prediction-age-days", 3,
		"refuse a signal older than this many days; as-of-ness alone is not freshness")
	apply := flag.Bool("apply", false, "actually write the reconstruction (default: report and stop)")
	dbPath := flag.String("db", "", "database path (default: config)")
	flag.Parse()

	if *fromS == "" || *toS == "" {
		fmt.Fprintln(os.Stderr, "paperreplay: --from and --to are required")
		os.Exit(2)
	}
	from, err := parseDay(*fromS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "paperreplay: --from %v\n", err)
		os.Exit(2)
	}
	to, err := parseDay(*toS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "paperreplay: --to %v\n", err)
		os.Exit(2)
	}
	to += 86399 // inclusive of the whole end day, whatever hour its bar carries

	path := *dbPath
	if path == "" {
		path = config.Load().DBPath
	}
	st, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "paperreplay: open %s: %v\n", path, err)
		os.Exit(2)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	first, last, days, err := st.UniverseMembershipCoverage(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "paperreplay: universe coverage: %v\n", err)
		os.Exit(2)
	}
	bars, err := st.DailyBarTimesBetween(ctx, string(md.TF1d), from, to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "paperreplay: sessions: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("database            : %s\n", path)
	fmt.Printf("window              : %s .. %s\n", *fromS, *toS)
	fmt.Printf("sessions to replay  : %d\n", len(bars))
	fmt.Printf("universe record     : %s .. %s (%d days)\n",
		time.Unix(first, 0).UTC().Format("2006-01-02"),
		time.Unix(last, 0).UTC().Format("2006-01-02"), days)
	fmt.Printf("max signal age      : %d day(s)\n", *maxAgeDays)
	fmt.Printf("written under       : flagship-1d%s, flagship-1w%s\n\n", *suffix, *suffix)

	cfg := pipeline.ReplayConfig{
		StrategySuffix:   *suffix,
		MaxPredictionAge: int64(*maxAgeDays) * 86400,
	}
	if !*apply {
		fmt.Println("REPORT ONLY — pass --apply to write the reconstruction.")
		fmt.Println("The output is a RECONSTRUCTION, not a live track record: it is computed with")
		fmt.Println("today's code, the advisory expectancy prior is WITHHELD (its table has no")
		fmt.Println("history), and any session whose universe was never recorded is refused.")
		return
	}

	w := &pipeline.PaperTrader{St: st}
	rep, err := w.ReplayRange(ctx, from, to, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nREFUSED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("replayed %d session(s) into %v (%d lock retr(ies))\n", rep.Bars, rep.Strategies, rep.Retries)
	for _, s := range rep.Statuses {
		fmt.Println("  " + s)
	}
	fmt.Println("\nRECONSTRUCTION — not a live track record, and an UPPER BOUND.")
	fmt.Println("The live distribution runner rotates 60 symbols per pass, so only part of the")
	fmt.Println("universe held a forecast at any past instant (551 of ~1,048 on this database).")
	fmt.Println("This replay fits a fresh one for EVERY symbol at EVERY bar — more coverage than")
	fmt.Println("the system ever had, which admits more trades. The rotation's past position is")
	fmt.Println("not recorded, so it cannot be corrected, only stated.")
}
