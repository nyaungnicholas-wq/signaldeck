// iexhist-load builds the daily bars of an IEX-only SignalDeck database from the
// per-day CSVs written by iexhist-bars.
//
//	iexhist-load -dir data/iexhist -db data/public/signaldeck.db -top 400
//
// It always rebuilds from the raw archive: every run re-detects splits over the
// whole series and rewrites the adjusted bars, so a split that happens next
// month repairs the stored history on the next load. Upserts are idempotent.
//
// The database it writes must hold NO other provider's bars. It refuses a
// database that carries the Alpaca instance's marker rows.
//
// Data provided for free by IEX. https://www.iex.io/legal/hist-data-terms
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/iexhist"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const sourceMetaKey = store.PriceSourceMetaKey

func main() {
	dir := flag.String("dir", "", "directory of YYYYMMDD.csv day files")
	dbPath := flag.String("db", "", "SQLite database to write (created if absent)")
	top := flag.Int("top", 400, "universe size: symbols ranked by median IEX dollar volume")
	recent := flag.Int("recent", 60, "trading days the ranking looks at")
	coverage := flag.Float64("coverage", 0.9, "minimum share of all loaded days a symbol must have traded on")
	minDays := flag.Int("min-days", 260, "refuse to load fewer day files than this (the forecasts need about 230 bars)")
	dry := flag.Bool("dry-run", false, "report what would be loaded; write nothing")
	flag.Parse()
	if *dir == "" || (*dbPath == "" && !*dry) {
		fail(fmt.Errorf("give -dir and -db (or -dry-run)"))
	}

	files, err := filepath.Glob(filepath.Join(*dir, "20??????.csv"))
	if err != nil || len(files) == 0 {
		fail(fmt.Errorf("no day files in %s", *dir))
	}
	sort.Strings(files)
	series := map[string][]iexhist.Day{}
	var dates []string
	for _, f := range files {
		date := strings.TrimSuffix(filepath.Base(f), ".csv")
		// IEX publishes small test captures on some weekends. They are not
		// sessions; skipping by calendar keeps the partial-day refusal below
		// meaningful for real trading days.
		if d, err := time.Parse("20060102", date); err != nil {
			fail(fmt.Errorf("%s: not a YYYYMMDD day file", f))
		} else if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
			fmt.Printf("iexhist-load: skipping weekend file %s\n", date)
			continue
		}
		n, err := readDay(f, date, series)
		if err != nil {
			fail(fmt.Errorf("%s: %w", f, err))
		}
		if n < 1000 {
			// A real session has thousands of symbols. A short file is a
			// truncated decode and must not become a hole in every series.
			fail(fmt.Errorf("%s holds only %d symbols: refusing a partial day", f, n))
		}
		dates = append(dates, date)
	}

	if len(dates) < *minDays {
		fail(fmt.Errorf("only %d day files; -min-days is %d. A short history yields symbols no predictor can use", len(dates), *minDays))
	}
	universe := iexhist.PickUniverse(series, dates, *recent, *top, *coverage)
	fmt.Printf("iexhist-load: %d day files (%s..%s), %d symbols seen, universe %d\n",
		len(dates), dates[0], dates[len(dates)-1], len(series), len(universe))
	if len(universe) == 0 {
		fail(fmt.Errorf("no symbol met the coverage rule; load more days first"))
	}

	type built struct {
		sym  string
		days []iexhist.Day
	}
	var out []built
	splits := 0
	for _, sym := range universe {
		adj, applied := iexhist.AdjustSplits(series[sym])
		for _, a := range applied {
			fmt.Printf("  split %s %s\n", sym, a)
		}
		splits += len(applied)
		out = append(out, built{sym, adj})
	}
	fmt.Printf("iexhist-load: %d split adjustment(s) applied\n", splits)
	if *dry {
		fmt.Println("iexhist-load: dry run, nothing written")
		return
	}

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		fail(err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		fail(err)
	}
	defer st.Close()
	ctx := context.Background()

	// One price source per database, enforced where the bars are written.
	if cur, _ := st.GetMeta(ctx, sourceMetaKey); cur != "" && cur != "iexhist" {
		fail(fmt.Errorf("%s is marked price_source=%s: refusing to mix IEX bars into it", *dbPath, cur))
	}
	var other int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM bars WHERE tf != '1d'`).Scan(&other); err == nil && other > 0 {
		fail(fmt.Errorf("%s already holds %d intraday bars, which IEX history never writes: this is not an IEX-only database", *dbPath, other))
	}
	if err := st.SetMeta(ctx, sourceMetaKey, "iexhist"); err != nil {
		fail(err)
	}

	// Only the symbols loaded here stay active. A name that fell out of the
	// ranking, or one the daemon's first boot registered with no bars, would
	// otherwise sit active on frozen or absent history.
	keep := map[string]bool{}
	for _, b := range out {
		keep[b.sym] = true
	}
	all, err := st.ListSymbols(ctx, false)
	if err != nil {
		fail(err)
	}
	dropped := 0
	for _, s := range all {
		if s.Market == md.Stocks && s.Active && !keep[s.Symbol] {
			if err := st.SetSymbolActive(ctx, s.ID, false); err != nil {
				fail(err)
			}
			dropped++
		}
	}
	fmt.Printf("iexhist-load: %d symbol(s) outside the universe set inactive\n", dropped)

	written := 0
	for _, b := range out {
		sym, err := st.UpsertSymbol(ctx, b.sym, md.Stocks, "")
		if err != nil {
			fail(fmt.Errorf("symbol %s: %w", b.sym, err))
		}
		bars := make([]md.Bar, 0, len(b.days))
		for _, d := range b.days {
			ts, err := iexhist.BarTs(d.Date)
			if err != nil {
				fail(err)
			}
			if d.Close <= 0 || d.High < d.Low {
				continue
			}
			bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: ts,
				Open: d.Open, High: d.High, Low: d.Low, Close: d.Close, Volume: float64(d.Volume)})
		}
		if err := st.UpsertBars(ctx, bars); err != nil {
			fail(fmt.Errorf("bars %s: %w", b.sym, err))
		}
		written += len(bars)
	}
	fmt.Printf("iexhist-load: OK %d daily bars for %d symbols written to %s\n", written, len(out), *dbPath)
}

func readDay(path, date string, series map[string][]iexhist.Day) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 || len(rows[0]) < 7 || rows[0][0] != "symbol" {
		return 0, fmt.Errorf("unexpected header")
	}
	n := 0
	for _, r := range rows[1:] {
		if len(r) < 7 {
			continue
		}
		o, e1 := strconv.ParseFloat(r[1], 64)
		h, e2 := strconv.ParseFloat(r[2], 64)
		l, e3 := strconv.ParseFloat(r[3], 64)
		c, e4 := strconv.ParseFloat(r[4], 64)
		v, e5 := strconv.ParseInt(r[5], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
			return 0, fmt.Errorf("bad row for %s", r[0])
		}
		series[r[0]] = append(series[r[0]], iexhist.Day{Date: date,
			Bar: iexhist.Bar{Open: o, High: h, Low: l, Close: c, Volume: v}})
		n++
	}
	return n, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "iexhist-load: FAILED:", err)
	os.Exit(1)
}
