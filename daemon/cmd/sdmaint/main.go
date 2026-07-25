// sdmaint — offline database maintenance for SignalDeck.
//
// Run with the daemon STOPPED (zero readers) so it can do in one uncontended
// pass what the hourly workers can't finish under live read pressure:
//   - scores + composite_scores: archive-then-strip the heavy JSON blobs
//     (the exact ScoresCompactor path — archive-before-strip fail-safe, no row
//     deletion unless a row is older than the intraday window)
//   - WAL checkpoint(TRUNCATE) — collapses the WAL file the live governor
//     can never truncate while readers hold it
//   - VACUUM — returns the freed pages to the filesystem
//
// It reuses the daemon's own store + archive packages, so the cold-archive
// format and every safety invariant match production exactly. Reversible: the
// stripped blobs are written to data/archive/*.csv.gz first, and a full DB
// backup predates the run.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/maintain"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func main() {
	dbPath := flag.String("db", "data/signaldeck.db", "path to signaldeck.db")
	heavyD := flag.Int("heavy", 2, "keep full component/payload blobs this many days")
	intradayD := flag.Int("intraday", 30, "keep intraday rows this many days (daily-downsample beyond)")
	doVacuum := flag.Bool("vacuum", true, "VACUUM after compaction")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close() //nolint:errcheck

	before, walBefore := st.FileSizes()
	fmt.Printf("before: db=%.0fMB wal=%.0fMB\n", mb(before), mb(walBefore))

	arc := archive.New(archive.Dir(*dbPath))
	ctx := context.Background()

	c := &maintain.ScoresCompactor{
		St:           st,
		Arc:          arc,
		KeepHeavy:    time.Duration(*heavyD) * 24 * time.Hour,
		KeepIntraday: time.Duration(*intradayD) * 24 * time.Hour,
	}
	fmt.Println("compacting scores + composite_scores (offline, uncontended)…")
	start := time.Now()
	detail, err := c.Run(ctx)
	if err != nil {
		log.Fatalf("compact: %v", err)
	}
	fmt.Printf("compaction: %s (%.1fs)\n", detail, time.Since(start).Seconds())

	if res, err := st.WALCheckpointTruncate(ctx); err != nil {
		log.Printf("checkpoint: %v", err)
	} else {
		fmt.Printf("wal checkpoint: %+v\n", res)
	}

	if *doVacuum {
		fmt.Println("VACUUM (rewrites the file — this is the slow part)…")
		vs := time.Now()
		if err := st.Vacuum(ctx); err != nil {
			log.Fatalf("vacuum: %v", err)
		}
		fmt.Printf("vacuum done (%.1fs)\n", time.Since(vs).Seconds())
	}

	after, walAfter := st.FileSizes()
	fmt.Printf("after:  db=%.0fMB wal=%.0fMB  (freed %.0fMB)\n",
		mb(after), mb(walAfter), mb(before-after))
}

func mb(b int64) float64 { return float64(b) / (1024 * 1024) }
