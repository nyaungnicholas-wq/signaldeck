// seldiff answers one question: which research rules change verdict if the discovery
// screen selects on mean signed return instead of on a Wilson lower bound of the weekly
// hit rate? It runs researchx.Discover TWICE over the identical corpus at the identical
// corrected alpha, varying only DiscoverConfig.SelectBy, and diffs the survivor sets.
// Reads the database mode=ro and writes nothing.
//
//	go run ./cmd/seldiff
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"

	_ "modernc.org/sqlite"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
)

var (
	dbPath = flag.String("db", `C:\Users\Nicholas_N\Desktop\claude code\signaldeck\data\signaldeck.db`, "path to SQLite database")
	prior  = flag.Int("prior", -1, "PriorSearches override (-1 = read from DB)")
)

func main() {
	flag.Parse()
	conn, err := sql.Open("sqlite", "file:"+*dbPath+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stdout, "open db: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close() //nolint:errcheck

	// Load corpus
	var maxTs int64
	if err := conn.QueryRow("SELECT MAX(ts) FROM research_weeks").Scan(&maxTs); err != nil {
		_, _ = fmt.Fprintf(os.Stdout, "maxTs: %v\n", err)
		os.Exit(1)
	}
	toWeek := maxTs / histfeat.WeekSecs

	projection := []string{
		"pressure_score", "pressure_abs", "comp_rsi", "rsi_pct", "rsi14",
		"ext_score", "vol_pct", "vol_anomaly", "price_accel", "consec_dir",
		"vwap_dist_atr", "atr_ext_20", "vix_high_vol", "mkt_trend",
	}
	projSet := make(map[string]bool, len(projection))
	for _, k := range projection {
		projSet[k] = true
	}

	rows, err := conn.Query(`
		SELECT symbol_id, week, ts, vec, fwd_return, up, era, high_vol
		FROM research_weeks WHERE week>=0 AND week<=? ORDER BY week, symbol_id`,
		toWeek,
	)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stdout, "query research_weeks: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close() //nolint:errcheck

	var (
		obs      []researchx.Obs
		lastWeek int64 = math.MinInt64
		perSym         = make(map[int64]int64)
		scanned  int
		dropped  int
	)

	for rows.Next() {
		scanned++
		var (
			symbolID  int64
			week      int64
			ts        int64
			vecJSON   []byte
			fwdReturn float64
			up        int64
			era       string
			highVol   int64
		)
		if err := rows.Scan(&symbolID, &week, &ts, &vecJSON, &fwdReturn, &up, &era, &highVol); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "scan research_weeks: %v\n", err)
			os.Exit(1)
		}

		// Sentinel checks
		drop := false
		if week < lastWeek {
			drop = true
		} else if prev, ok := perSym[symbolID]; ok && week <= prev {
			drop = true
		} else if math.Abs(fwdReturn) > 1.5 {
			drop = true
		}
		if drop {
			dropped++
			continue
		}
		lastWeek = week
		perSym[symbolID] = week

		var vec map[string]float64
		if err := json.Unmarshal(vecJSON, &vec); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "unmarshal vec: %v\n", err)
			os.Exit(1)
		}
		// Project to allowed keys
		projVec := make(map[string]float64, len(projection))
		for k, v := range vec {
			if projSet[k] {
				projVec[k] = v
			}
		}
		if v, ok := projVec["comp_rsi"]; ok {
			projVec["comp_rsi_abs"] = math.Abs(v)
		}

		obs = append(obs, researchx.Obs{
			SymbolID: symbolID,
			Week:     week,
			Ts:       ts,
			Vec:      projVec,
			Up:       up != 0,
			FwdRet:   fwdReturn,
			Era:      era,
			HighVol:  highVol != 0,
		})
	}
	if err := rows.Err(); err != nil {
		_, _ = fmt.Fprintf(os.Stdout, "rows err: %v\n", err)
		os.Exit(1)
	}

	// Determine prior searches
	p := *prior
	priorSource := "flag"
	if p < 0 {
		priorSource = "database"
		var val string
		if err := conn.QueryRow("SELECT v FROM meta WHERE k='research_loop_searches'").Scan(&val); err != nil {
			if err != sql.ErrNoRows {
				_, _ = fmt.Fprintf(os.Stdout, "meta query: %v\n", err)
				os.Exit(1)
			}
			p = 0
		} else {
			v, err := strconv.Atoi(val)
			if err != nil {
				p = 0
			} else {
				p = v
			}
		}
	}
	if p < 0 {
		p = 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "PriorSearches: %d (source: %s)\n", p, priorSource)

	cfg := researchx.DiscoverConfig{
		PriorSearches: p,
		HoldoutEra:    researchx.PreregHoldoutEra,
	}
	// Both runs must use the same config except SelectBy

	// Run Wilson
	wilsonCfg := cfg
	wilsonCfg.SelectBy = researchx.SelectWilson
	wilsonResults := researchx.Discover(obs, wilsonCfg)

	// Run MeanRet
	meanRetCfg := cfg
	meanRetCfg.SelectBy = researchx.SelectMeanRet
	meanRetResults := researchx.Discover(obs, meanRetCfg)

	// Index meanRet results by ID
	meanRetByID := make(map[string]researchx.Candidate, len(meanRetResults))
	for _, c := range meanRetResults {
		meanRetByID[c.ID] = c
	}

	// a) Header
	divisor := cfg.Divisor()
	alpha := cfg.CorrectedAlpha()
	_, _ = fmt.Fprintf(os.Stdout, "\n=== seldiff Report ===\n")
	_, _ = fmt.Fprintf(os.Stdout, "Rows scanned: %d\n", scanned)
	_, _ = fmt.Fprintf(os.Stdout, "Rows dropped (sentinel): %d\n", dropped)
	_, _ = fmt.Fprintf(os.Stdout, "Obs kept: %d\n", len(obs))
	_, _ = fmt.Fprintf(os.Stdout, "Distinct rules judged: %d\n", len(wilsonResults))
	_, _ = fmt.Fprintf(os.Stdout, "Divisor: %d\n", divisor)
	_, _ = fmt.Fprintf(os.Stdout, "Corrected alpha: %.4f\n", alpha)

	// b) Survivors
	var wilsonSurv, meanRetSurv []string
	for _, c := range wilsonResults {
		if c.Survives {
			wilsonSurv = append(wilsonSurv, c.ID)
		}
	}
	for _, c := range meanRetResults {
		if c.Survives {
			meanRetSurv = append(meanRetSurv, c.ID)
		}
	}
	sort.Strings(wilsonSurv)
	sort.Strings(meanRetSurv)
	_, _ = fmt.Fprintf(os.Stdout, "\nSurvivors under Wilson: %d\n", len(wilsonSurv))
	for _, id := range wilsonSurv {
		_, _ = fmt.Fprintf(os.Stdout, "  %s\n", id)
	}
	_, _ = fmt.Fprintf(os.Stdout, "Survivors under MeanRet: %d\n", len(meanRetSurv))
	for _, id := range meanRetSurv {
		_, _ = fmt.Fprintf(os.Stdout, "  %s\n", id)
	}

	// c) Diff
	wilsonSet := make(map[string]bool, len(wilsonSurv))
	for _, id := range wilsonSurv {
		wilsonSet[id] = true
	}
	meanRetSet := make(map[string]bool, len(meanRetSurv))
	for _, id := range meanRetSurv {
		meanRetSet[id] = true
	}

	var onlyWilson, onlyMeanRet []string
	for id := range wilsonSet {
		if !meanRetSet[id] {
			onlyWilson = append(onlyWilson, id)
		}
	}
	for id := range meanRetSet {
		if !wilsonSet[id] {
			onlyMeanRet = append(onlyMeanRet, id)
		}
	}
	sort.Strings(onlyWilson)
	sort.Strings(onlyMeanRet)

	_, _ = fmt.Fprintf(os.Stdout, "\n=== THE DIFF ===\n")
	if len(wilsonSurv) == 0 && len(meanRetSurv) == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "Both survivor sets are empty: no rules survive under either criterion.\n")
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "Only Wilson: %d rules\n", len(onlyWilson))
		for _, id := range onlyWilson {
			// Find Wilson candidate
			var wCand researchx.Candidate
			for _, c := range wilsonResults {
				if c.ID == id {
					wCand = c
					break
				}
			}
			// Reject gate from MeanRet run
			var rejectGate string
			if mc, ok := meanRetByID[id]; ok {
				rejectGate = mc.RejectedBy
			}
			desc := wCand.Desc
			if len(desc) > 46 {
				desc = desc[:46]
			}
			_, _ = fmt.Fprintf(os.Stdout, "%-30s WilsonLower=%.4f NullP0=%.4f MeanRet=%.4f MeanRetT=%.2f weeks=%d winWeeks=%d rejectedBy=%s %s\n",
				id, wCand.WilsonLower, wCand.NullP0, wCand.MeanRet, wCand.MeanRetT,
				wCand.Grade.Weeks, wCand.Grade.WinWeeks, rejectGate, desc)
		}
		_, _ = fmt.Fprintf(os.Stdout, "Only MeanRet: %d rules\n", len(onlyMeanRet))
		for _, id := range onlyMeanRet {
			mc := meanRetByID[id]
			// Find Wilson candidate for RejectedBy
			var wReject string
			for _, c := range wilsonResults {
				if c.ID == id {
					wReject = c.RejectedBy
					break
				}
			}
			desc := mc.Desc
			if len(desc) > 46 {
				desc = desc[:46]
			}
			_, _ = fmt.Fprintf(os.Stdout, "%-30s WilsonLower=%.4f NullP0=%.4f MeanRet=%.4f MeanRetT=%.2f weeks=%d winWeeks=%d rejectedBy=%s %s\n",
				id, mc.WilsonLower, mc.NullP0, mc.MeanRet, mc.MeanRetT,
				mc.Grade.Weeks, mc.Grade.WinWeeks, wReject, desc)
		}
		both := 0
		for id := range wilsonSet {
			if meanRetSet[id] {
				both++
			}
		}
		_, _ = fmt.Fprintf(os.Stdout, "Both: %d rules\n", both)
	}

	// d) Rejection-gate histogram
	_, _ = fmt.Fprintf(os.Stdout, "\n=== REJECTION-GATE HISTOGRAM ===\n")
	histogram := func(results []researchx.Candidate, label string) {
		gateCounts := make(map[string]int)
		for _, c := range results {
			if !c.Survives {
				gate := c.RejectedBy
				if gate == "" {
					gate = "-"
				}
				gateCounts[gate]++
			}
		}
		type gateCount struct {
			gate  string
			count int
		}
		var sorted []gateCount
		for g, c := range gateCounts {
			sorted = append(sorted, gateCount{g, c})
		}
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].count != sorted[j].count {
				return sorted[i].count > sorted[j].count
			}
			return sorted[i].gate < sorted[j].gate
		})
		_, _ = fmt.Fprintf(os.Stdout, "%s:\n", label)
		for _, gc := range sorted {
			_, _ = fmt.Fprintf(os.Stdout, "  %-20s %d\n", gc.gate, gc.count)
		}
	}
	histogram(wilsonResults, "Wilson")
	histogram(meanRetResults, "MeanRet")

	// e) Full table
	_, _ = fmt.Fprintf(os.Stdout, "\n=== FULL TABLE ===\n")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "ID\tweeks\twinWeeks\twinRate\twilsonLower\tnullP0\tmeanRet(bp/wk)\tmeanRetT\tverdict\n")

	// Index both for quick lookup
	wilsonByID := make(map[string]researchx.Candidate, len(wilsonResults))
	for _, c := range wilsonResults {
		wilsonByID[c.ID] = c
	}

	// Build combined list of all judged rules
	var allIDs []string
	for id := range wilsonByID {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)

	for _, id := range allIDs {
		w, wOK := wilsonByID[id]
		m, mOK := meanRetByID[id]
		if !mOK {
			continue
		}
		var weeks, winWeeks int
		var winRate, wilsonLower, nullP0, meanRet, meanRetT float64
		var vW, vM bool
		if wOK {
			weeks = w.Grade.Weeks
			winWeeks = w.Grade.WinWeeks
			if weeks > 0 {
				winRate = float64(winWeeks) / float64(weeks)
			}
			wilsonLower = w.WilsonLower
			nullP0 = w.NullP0
			meanRet = w.MeanRet
			meanRetT = w.MeanRetT
			vW = w.Survives
		}
		if mOK {
			if !wOK || weeks == 0 {
				weeks = m.Grade.Weeks
				winWeeks = m.Grade.WinWeeks
				if weeks > 0 {
					winRate = float64(winWeeks) / float64(weeks)
				}
				wilsonLower = m.WilsonLower
				nullP0 = m.NullP0
				meanRet = m.MeanRet
				meanRetT = m.MeanRetT
			}
			vM = m.Survives
		}

		verdict := "--"
		if vW && vM {
			verdict = "WM"
		} else if vW {
			verdict = "W-"
		} else if vM {
			verdict = "-M"
		}

		meanRetBP := meanRet * 10000
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%.4f\t%.4f\t%.4f\t%.4f\t%.2f\t%s",
			id, weeks, winWeeks, winRate, wilsonLower, nullP0, meanRetBP, meanRetT, verdict)
		if vW != vM {
			_, _ = fmt.Fprintf(tw, "  <-- DISAGREES")
		}
		_, _ = fmt.Fprintln(tw)
	}
	_ = tw.Flush()
}
