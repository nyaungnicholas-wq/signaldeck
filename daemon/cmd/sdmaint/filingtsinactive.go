package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// repairFilingTsInactive runs the D-004 acceptance-time repair (the same
// CorrectFilingTs the filings sweep uses) for INACTIVE stock symbols, which the
// sweep never fetches. The issuer CIK is read from the stored filing URLs, not
// from the current ticker map, so a reused ticker cannot point at the wrong
// company; and CorrectFilingTs only touches accessions present in the fetched
// feed, which are globally unique. Dry run (no network, no writes) unless -apply.
func repairFilingTsInactive(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("repair-filing-ts-inactive", flag.ContinueOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	apply := fs.Bool("apply", false, "fetch EDGAR and repair (default: dry run)")
	pause := fs.Duration("pause", time.Second, "sleep between EDGAR requests")
	limit := fs.Int("limit", 0, "process at most this many symbols (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*dbPath); err != nil {
		return fmt.Errorf("-db %s: %w (refusing to create a new database)", *dbPath, err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	rows, err := st.DB().QueryContext(ctx, `SELECT s.id, s.symbol, f.url FROM symbols s
		JOIN filings f ON f.symbol_id = s.id
		WHERE s.active = 0 AND s.market = 'stocks' AND f.url LIKE '%/Archives/edgar/data/%'`)
	if err != nil {
		return err
	}
	type cand struct {
		symbol string
		ciks   map[int64]int
	}
	byID := map[int64]*cand{}
	for rows.Next() {
		var id int64
		var sym, url string
		if err := rows.Scan(&id, &sym, &url); err != nil {
			rows.Close() //nolint:errcheck
			return err
		}
		cik, ok := cikFromURL(url)
		if !ok {
			continue
		}
		c := byID[id]
		if c == nil {
			c = &cand{symbol: sym, ciks: map[int64]int{}}
			byID[id] = c
		}
		c.ciks[cik]++
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	type target struct {
		id     int64
		symbol string
		cik    int64
	}
	var ts []target
	for id, c := range byID {
		var best int64
		bestN := 0
		for k, n := range c.ciks {
			if n > bestN || (n == bestN && k < best) {
				best, bestN = k, n
			}
		}
		ts = append(ts, target{id, c.symbol, best})
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].id < ts[j].id })
	if *limit > 0 && len(ts) > *limit {
		ts = ts[:*limit]
	}

	if !*apply {
		for _, t := range ts {
			fmt.Fprintf(out, "DRY %s id=%d cik=%d\n", t.symbol, t.id, t.cik) //nolint:errcheck
		}
		_, err := fmt.Fprintf(out, "DRYRUN symbols=%d (pass -apply to fetch EDGAR and repair)\n", len(ts))
		return err
	}

	client := edgar.NewFilings()
	fetched, errs, total := 0, 0, 0
	for i, t := range ts {
		if i > 0 {
			time.Sleep(*pause)
		}
		subs, _, err := client.SubmissionsWithProfile(ctx, t.cik)
		if err != nil {
			errs++
			fmt.Fprintf(out, "ERR %s cik=%d: %v\n", t.symbol, t.cik, err) //nolint:errcheck
			continue
		}
		fetched++
		feed := make(map[string]int64, len(subs))
		for _, f := range subs {
			if f.Accession != "" {
				feed[f.Accession] = f.FiledTs
			}
		}
		n, err := st.CorrectFilingTs(ctx, t.id, feed, time.Now().Unix())
		if err != nil {
			return fmt.Errorf("%s: %w", t.symbol, err)
		}
		if n > 0 {
			total += n
			fmt.Fprintf(out, "FIXED %s %d\n", t.symbol, n) //nolint:errcheck
		}
	}
	_, err = fmt.Fprintf(out, "APPLIED symbols=%d fetched=%d errors=%d corrected=%d\n", len(ts), fetched, errs, total)
	return err
}

// cikFromURL returns the issuer CIK in .../Archives/edgar/data/<cik>/...
func cikFromURL(url string) (int64, bool) {
	const marker = "/Archives/edgar/data/"
	i := strings.Index(url, marker)
	if i < 0 {
		return 0, false
	}
	seg := url[i+len(marker):]
	if j := strings.IndexByte(seg, '/'); j >= 0 {
		seg = seg[:j]
	}
	cik, err := strconv.ParseInt(seg, 10, 64)
	return cik, err == nil && cik > 0
}
