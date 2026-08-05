package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// purgeBadBars deletes bars the vendor returned without a price and re-dates
// the symbols that lose their leading rows. See internal/store/badbars.go for
// why a zero close is an absent price rather than a trade at zero.
func purgeBadBars(args []string) error {
	fs := flag.NewFlagSet("purge-bad-bars", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	dryRun := fs.Bool("dry-run", false, "report what would be deleted, write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	var n, syms int64
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(DISTINCT symbol_id) FROM bars WHERE close <= 0`).
		Scan(&n, &syms); err != nil {
		return err
	}
	fmt.Printf("bars with a non-positive close: %d across %d symbol(s)\n", n, syms)
	if n == 0 {
		return nil
	}
	if *dryRun {
		fmt.Println("dry-run: nothing written")
		return nil
	}

	deleted, redated, err := st.PurgeNonPositiveBars(ctx, 0)
	if err != nil {
		return err
	}
	fmt.Printf("deleted %d bar(s); re-dated added_at on %d symbol(s)\n", deleted, redated)
	return nil
}
