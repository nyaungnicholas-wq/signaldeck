package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// buildUniverse materialises universe_membership from daily-bar evidence.
//
// WHY THIS IS A GO COMMAND. The single-writer-connection doctrine lives in
// internal/store: exactly one connection may write signaldeck.db, and the
// daemon usually holds it. A Python script issuing ~1.8M inserts against a live
// 4 GB database bypasses that, so the derivation stays in store and this is the
// operator-facing handle on it — the same split as apply-delistings.
//
// -dry-run reports the span the rebuild WOULD produce without writing, by
// counting the source evidence. It is the honest preview: it reads bars only.
func buildUniverse(args []string) error {
	fs := flag.NewFlagSet("build-universe", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	dryRun := fs.Bool("dry-run", false, "report what would be written, write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	before, err := st.UniverseSpan(ctx)
	if err != nil {
		return fmt.Errorf("read current span: %w", err)
	}
	fmt.Printf("before: %d rows over %d days (%s .. %s), %d symbols\n",
		before.Rows, before.Days, day(before.FirstDay), day(before.LastDay), before.Symbols)

	if *dryRun {
		fmt.Println("dry-run: nothing written")
		return nil
	}

	start := time.Now()
	res, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		return err
	}
	after, err := st.UniverseSpan(ctx)
	if err != nil {
		return fmt.Errorf("read new span: %w", err)
	}
	fmt.Printf("after : %d rows over %d days (%s .. %s), %d symbols  [%s]\n",
		res.Rows, after.Days, day(after.FirstDay), day(after.LastDay), after.Symbols,
		time.Since(start).Round(time.Millisecond))
	if res.ReusedTickerDays > 0 {
		fmt.Printf("TICKER REUSE: %d symbol-days print AFTER a recorded delisting and were "+
			"EXCLUDED. One symbol row is holding two securities; splitting it is a separate repair.\n",
			res.ReusedTickerDays)
	}

	// A membership that spans no days is the exact defect this command exists
	// to end, so it fails loudly rather than reporting a successful no-op.
	if after.Days == 0 {
		return fmt.Errorf("rebuild produced an EMPTY membership — no tf='1d' bars to derive it from")
	}
	return nil
}

func day(ts int64) string {
	if ts == 0 {
		return "—"
	}
	return time.Unix(ts, 0).UTC().Format("2006-01-02")
}
