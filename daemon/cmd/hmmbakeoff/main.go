// Command hmmbakeoff grades the hmmregime HMM against the incumbent rule-based
// regime labeller and against a trailing realized-volatility tercile, on stored
// daily bars.
//
// The question it answers is narrow and falsifiable: does a label tell you
// anything about the size of the NEXT day's move that you did not already know?
// A volatility regime is not a directional forecast, so it is graded on forward
// absolute return and nothing else.
//
// Three rules keep the comparison honest:
//
//   - OUT OF SAMPLE. The HMM is fitted on the first 60% of each symbol's bars
//     and graded only on the last 40%. Its parameters never see the test window.
//     The incumbent and the tercile baseline are parameter-free, so the same
//     split is applied to all three and nobody gets a training-set advantage.
//
//   - NO LOOKAHEAD. Every label at bar i is computed from bars[0..i] only, and
//     the target is |log(C[i+1]/C[i])|, strictly forward of the label.
//
//   - PER-SYMBOL NORMALISATION. Each symbol's forward move is divided by that
//     symbol's own mean forward move over its test window before pooling. Raw
//     pooling would measure the spread between quiet and wild SYMBOLS, which
//     every labeller would "predict" trivially; dividing it out leaves only
//     within-symbol discrimination, which is the thing being tested.
//
// It opens the database read-only and writes nothing.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"

	_ "modernc.org/sqlite"

	"github.com/nyaungnicholas-wq/signaldeck/internal/hmmregime"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/regime"
)

const (
	trainFrac = 0.60
	volWindow = 20  // trailing bars for the realized-vol baseline
	volLookbk = 250 // trailing window the tercile cutoffs are taken over
)

// bucket accumulates normalised forward moves for one label.
type bucket struct {
	sum float64
	n   int
}

func (b *bucket) add(x float64) { b.sum += x; b.n++ }
func (b bucket) mean() float64 {
	if b.n == 0 {
		return math.NaN()
	}
	return b.sum / float64(b.n)
}

// labeller results keyed by label name.
type tally map[string]*bucket

func (t tally) add(label string, x float64) {
	b, ok := t[label]
	if !ok {
		b = &bucket{}
		t[label] = b
	}
	b.add(x)
}

func main() {
	dbPath := flag.String("db", "../data/signaldeck.db", "path to signaldeck.db")
	minBars := flag.Int("minbars", 500, "minimum daily bars a symbol needs to be graded")
	maxSyms := flag.Int("maxsyms", 400, "cap on symbols graded (0 = all)")
	states := flag.Int("states", 2, "HMM state count")
	flag.Parse()

	db, err := sql.Open("sqlite", "file:"+*dbPath+"?mode=ro")
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer db.Close() //nolint:errcheck

	ids, err := eligibleSymbols(db, *minBars, *maxSyms)
	if err != nil {
		fmt.Fprintln(os.Stderr, "symbols:", err)
		os.Exit(1)
	}
	fmt.Printf("grading %d symbols (>=%d daily bars), HMM states=%d, train=%.0f%%\n\n",
		len(ids), *minBars, *states, trainFrac*100)

	hmmT, incT, volT := tally{}, tally{}, tally{}
	var used, skipped int

	cfg := hmmregime.Defaults()
	cfg.NStates = *states

	for _, id := range ids {
		bars, err := loadBars(db, id)
		if err != nil || len(bars) < *minBars {
			skipped++
			continue
		}
		split := int(float64(len(bars)) * trainFrac)

		model, ok := hmmregime.Fit(bars[:split], cfg)
		if !ok {
			skipped++
			continue
		}
		hmmPts := model.Filter(bars)

		incTimeline, _ := regime.History(bars, 1)
		incByTs := make(map[int64]regime.Label, len(incTimeline))
		for _, p := range incTimeline {
			incByTs[p.Ts] = p.Label
		}

		volLbl := volTercileLabels(bars)

		// Per-symbol normaliser: mean forward absolute move over the test window.
		var fsum float64
		var fn int
		for i := split; i < len(bars)-1; i++ {
			fsum += fwdAbs(bars, i)
			fn++
		}
		if fn == 0 || fsum <= 0 {
			skipped++
			continue
		}
		norm := fsum / float64(fn)

		for i := split; i < len(bars)-1; i++ {
			x := fwdAbs(bars, i) / norm
			hmmT.add(string(hmmPts[i].Label), x)
			if l, ok := incByTs[bars[i].Ts]; ok {
				incT.add(string(l), x)
			}
			if volLbl[i] != "" {
				volT.add(volLbl[i], x)
			}
		}
		used++
	}

	fmt.Printf("symbols graded: %d, skipped: %d\n", used, skipped)
	fmt.Println("\nmean forward |return|, as a multiple of each symbol's own test-window average.")
	fmt.Println("1.00 = a completely uninformative label. Spread is the whole point.")

	report("HMM (hmmregime, fitted out-of-sample)", hmmT)
	report("INCUMBENT (internal/regime, rule-based)", incT)
	report("BASELINE (trailing 20d realized-vol tercile)", volT)
}

func report(title string, t tally) {
	fmt.Printf("\n%s\n", title)
	if len(t) == 0 {
		fmt.Println("  (no observations)")
		return
	}
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return t[keys[a]].mean() < t[keys[b]].mean() })

	fmt.Printf("  %-12s %12s %12s\n", "label", "mean fwd|r|", "n")
	for _, k := range keys {
		fmt.Printf("  %-12s %12.3f %12d\n", k, t[k].mean(), t[k].n)
	}
	lo, hi := t[keys[0]], t[keys[len(keys)-1]]
	if lo.n > 0 && hi.n > 0 && lo.mean() > 0 {
		fmt.Printf("  separation (highest/lowest label) = %.2fx\n", hi.mean()/lo.mean())
	}
}

// fwdAbs is |log(C[i+1]/C[i])| — strictly forward of a label computed at i.
func fwdAbs(bars []marketdata.Bar, i int) float64 {
	if bars[i].Close <= 0 || bars[i+1].Close <= 0 {
		return 0
	}
	return math.Abs(math.Log(bars[i+1].Close / bars[i].Close))
}

// volTercileLabels is the cheap baseline the HMM has to beat: trailing realized
// volatility bucketed against its OWN trailing distribution. Both windows end at
// i, so the label at i uses no future data. Bars without enough history get "".
func volTercileLabels(bars []marketdata.Bar) []string {
	n := len(bars)
	out := make([]string, n)
	rv := make([]float64, n)
	for i := range rv {
		rv[i] = math.NaN()
	}
	for i := volWindow; i < n; i++ {
		var s float64
		for j := i - volWindow + 1; j <= i; j++ {
			if bars[j-1].Close > 0 && bars[j].Close > 0 {
				r := math.Log(bars[j].Close / bars[j-1].Close)
				s += r * r
			}
		}
		rv[i] = math.Sqrt(s / float64(volWindow))
	}
	for i := volWindow + volLookbk; i < n; i++ {
		hist := make([]float64, 0, volLookbk)
		for j := i - volLookbk + 1; j <= i; j++ {
			if !math.IsNaN(rv[j]) {
				hist = append(hist, rv[j])
			}
		}
		if len(hist) < 30 {
			continue
		}
		sort.Float64s(hist)
		lo := hist[len(hist)/3]
		hi := hist[2*len(hist)/3]
		switch {
		case rv[i] <= lo:
			out[i] = "vol_low"
		case rv[i] >= hi:
			out[i] = "vol_high"
		default:
			out[i] = "vol_mid"
		}
	}
	return out
}

func eligibleSymbols(db *sql.DB, minBars, max int) ([]int64, error) {
	q := `SELECT symbol_id FROM bars WHERE tf='1d'
	      GROUP BY symbol_id HAVING COUNT(*) >= ?
	      ORDER BY COUNT(*) DESC`
	if max > 0 {
		q += fmt.Sprintf(" LIMIT %d", max)
	}
	rows, err := db.Query(q, minBars)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func loadBars(db *sql.DB, symbolID int64) ([]marketdata.Bar, error) {
	rows, err := db.Query(`SELECT ts, open, high, low, close, volume
	                       FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts`, symbolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []marketdata.Bar
	for rows.Next() {
		var b marketdata.Bar
		if err := rows.Scan(&b.Ts, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, err
		}
		b.SymbolID = symbolID
		out = append(out, b)
	}
	return out, rows.Err()
}
