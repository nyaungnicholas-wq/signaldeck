package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// adjudicateDelistings classifies every bar that sits AFTER its symbol's
// delisted_at, and repairs the two things that are actually wrong.
//
// WHAT THE 362 BARS TURNED OUT TO BE. Measured 2026-08-21 on the live database:
// 362 bars across 46 symbols carry a date strictly after their delisted_at day.
// They are not one phenomenon and must not get one treatment.
//
//	GENUINE TRADING (the majority of bars). BCSAU printed 11.14 on its stamped
//	last day and 11.22 eleven weeks later, then traded 533, 1589 and 1004 shares
//	over the following sessions. MITAU went 10.14 -> 10.96, APXIU 10.73 -> 11.90
//	across fourteen months, IRAAU 10.54 -> 11.21. Those are SPAC units accreting
//	trust value at a plausible rate, with real volume, at a continuous price.
//	They are the SAME instrument: the gap is a hole in the vendor's coverage, and
//	the delisted_at stamp — derived from the last bar the vendor happened to
//	return — is simply WRONG. Deleting these bars would be deleting market
//	history to protect a bad stamp. The stamp moves; the bars stay.
//
//	VENDOR PADDING. CFFSU printed 12 sessions and BIOR 12 after their stamps,
//	every one at volume 0 with open=high=low=close and ZERO total volume across
//	the run. Nothing traded. These are quarantined, reversibly.
//
// WHAT THIS COMMAND CANNOT DO. It has no corporate-actions feed, so the repaired
// delisted_at is an OBSERVATIONAL LOWER BOUND — "this security was still
// printing genuine volume on this date" — not a claim about a Form 25. Every
// symbol it touches is reported with the evidence that moved it, and symbols
// whose evidence is mixed are left alone and reported as ambiguous rather than
// guessed at.
//
// DRY RUN BY DEFAULT.
func adjudicateDelistings(args []string) error {
	fs := flag.NewFlagSet("adjudicate-delistings", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	apply := fs.Bool("apply", false, "write the reclassification (default: report only)")
	runID := fs.String("run-id", "", "quarantine run id for padded post-delisting bars (default: postdelist-<unix>)")
	restore := fs.String("restore", "", "restore a previous quarantine run by id and exit")
	out := fs.String("out", "", "write the full per-symbol evidence report to this JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	if *restore != "" {
		n, err := st.RestoreQuarantinedBars(ctx, *restore)
		if err != nil {
			return fmt.Errorf("restore %s: %w", *restore, err)
		}
		fmt.Printf("restored %d bar(s) from run %s back into bars\n", n, *restore)
		fmt.Println("NOTE: restoring bars does NOT roll back a delisted_at change; use the")
		fmt.Println("delisted_at_before field in the report to put those back by hand.")
		return nil
	}

	cases, err := st.PostDelistingCases(ctx, string(md.TF1d))
	if err != nil {
		return err
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Bars > cases[j].Bars })

	id := *runID
	if id == "" {
		id = fmt.Sprintf("postdelist-%d", time.Now().Unix())
	}

	type verdict struct {
		Symbol            string  `json:"symbol"`
		Name              string  `json:"name"`
		Classification    string  `json:"classification"`
		Evidence          string  `json:"evidence"`
		Bars              int     `json:"bars_after"`
		GenuineBars       int     `json:"genuine_bars"`
		PaddedBars        int     `json:"padded_bars"`
		Volume            float64 `json:"volume_after"`
		GapDays           int64   `json:"gap_days"`
		DelistedAtBefore  int64   `json:"delisted_at_before"`
		DelistedAtAfter   int64   `json:"delisted_at_after"`
		DelistedDayBefore string  `json:"delisted_day_before"`
		DelistedDayAfter  string  `json:"delisted_day_after"`
		Action            string  `json:"action"`
	}

	var verdicts []verdict
	var toQuarantine []store.PostDelistingCase
	var toRestamp []store.PostDelistingCase
	counts := map[string]int{}

	for _, c := range cases {
		v := verdict{
			Symbol: c.Symbol, Name: c.Name, Bars: c.Bars,
			GenuineBars: c.GenuineBars, PaddedBars: c.PaddedBars,
			Volume: c.VolumeAfter, GapDays: (c.FirstAfterTs - c.DelistedAt) / 86400,
			DelistedAtBefore:  c.DelistedAt,
			DelistedDayBefore: utcDay(c.DelistedAt),
		}
		switch {
		case c.GenuineBars == 0:
			// Nothing traded after the stamp. Pure padding.
			v.Classification = "vendor_pad"
			v.Evidence = fmt.Sprintf("%d bar(s) after the stamp, ALL zero-volume with open=high=low=close; "+
				"total volume across the run is 0", c.Bars)
			v.Action = "quarantine the padded bars; delisted_at unchanged"
			v.DelistedAtAfter, v.DelistedDayAfter = c.DelistedAt, utcDay(c.DelistedAt)
			toQuarantine = append(toQuarantine, c)
		case c.LastGenuineTs > c.DelistedAt:
			// Real prints after the stamp: the stamp is early.
			v.Classification = "still_trading"
			v.Evidence = fmt.Sprintf("%d of %d bar(s) after the stamp carry genuine volume (%.0f shares total); "+
				"last genuine print %s, %d day(s) after the stamped date",
				c.GenuineBars, c.Bars, c.VolumeAfter, utcDay(c.LastGenuineTs),
				(c.LastGenuineTs-c.DelistedAt)/86400)
			v.DelistedAtAfter = dayStart(c.LastGenuineTs)
			v.DelistedDayAfter = utcDay(v.DelistedAtAfter)
			v.Action = "move delisted_at forward to the last genuine print; quarantine any padded bars after it"
			toRestamp = append(toRestamp, c)
			if c.PaddedBars > 0 {
				toQuarantine = append(toQuarantine, c)
			}
		default:
			v.Classification = "ambiguous"
			v.Evidence = "post-stamp bars carry volume but no single genuine print could be resolved"
			v.Action = "left untouched; excluded from any claim requiring confirmed point-in-time membership"
			v.DelistedAtAfter, v.DelistedDayAfter = c.DelistedAt, utcDay(c.DelistedAt)
		}
		counts[v.Classification]++
		verdicts = append(verdicts, v)
	}

	fmt.Printf("database        : %s\n", *dbPath)
	fmt.Printf("post-delisting  : %d symbol(s), %d bar(s) strictly after the stamped day\n",
		len(cases), totalBars(cases))
	fmt.Printf("classification  : still_trading=%d vendor_pad=%d ambiguous=%d\n\n",
		counts["still_trading"], counts["vendor_pad"], counts["ambiguous"])

	fmt.Printf("%-10s %-14s %6s %8s %8s %12s %12s\n",
		"SYMBOL", "CLASS", "BARS", "GENUINE", "PADDED", "STAMP", "NEW STAMP")
	for _, v := range verdicts {
		newStamp := v.DelistedDayAfter
		if newStamp == v.DelistedDayBefore {
			newStamp = "-"
		}
		fmt.Printf("%-10s %-14s %6d %8d %8d %12s %12s\n",
			v.Symbol, v.Classification, v.Bars, v.GenuineBars, v.PaddedBars,
			v.DelistedDayBefore, newStamp)
	}

	if *out != "" {
		blob, err := json.MarshalIndent(map[string]any{
			"generated_utc": time.Now().UTC().Format(time.RFC3339),
			"database":      *dbPath,
			"run_id":        id,
			"applied":       *apply,
			"counts":        counts,
			"verdicts":      verdicts,
			"caveat": "delisted_at is repaired to an OBSERVATIONAL LOWER BOUND — the last session with genuine " +
				"volume. No corporate-actions feed was consulted; this is not a claim about a Form 25 date.",
		}, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*out, blob, 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("\nevidence report written to %s\n", *out)
	}

	if !*apply {
		fmt.Println("\nDRY RUN — nothing was changed. Re-run with -apply.")
		fmt.Printf("Padded bars would move to bars_quarantine under run id %s (reversible with -restore %s).\n", id, id)
		fmt.Printf("%d symbol(s) would have delisted_at moved forward to their last genuine print.\n", len(toRestamp))
		return nil
	}

	moved, err := st.QuarantinePostDelistingPads(ctx, string(md.TF1d), id,
		"post-delisting vendor pad: zero-volume open=high=low=close bar dated after the symbol stopped printing genuine volume",
		time.Now().Unix())
	if err != nil {
		return fmt.Errorf("quarantine pads: %w", err)
	}
	restamped, err := st.RestampDelistedAtFromGenuineBars(ctx, string(md.TF1d))
	if err != nil {
		return fmt.Errorf("restamp: %w", err)
	}

	fmt.Printf("\nquarantined %d padded post-delisting bar(s) under run id %s\n", moved, id)
	fmt.Printf("moved delisted_at forward on %d symbol(s)\n", restamped)
	fmt.Printf("undo the quarantine with: sdmaint adjudicate-delistings -restore %s\n", id)
	fmt.Println("rebuild the point-in-time universe next: sdmaint build-universe")
	if len(toQuarantine) > 0 && moved == 0 {
		fmt.Fprintln(os.Stderr, "WARNING: the report expected padded bars to move but none did. "+
			"Inspect bars_quarantine before trusting either number.")
	}
	return nil
}

func totalBars(cs []store.PostDelistingCase) int {
	n := 0
	for _, c := range cs {
		n += c.Bars
	}
	return n
}

// utcDay renders a unix second as a UTC date.
func utcDay(ts int64) string { return time.Unix(ts, 0).UTC().Format("2006-01-02") }

// dayStart truncates a bar ts to its UTC midnight, which is the convention
// delisted_at is stored in.
func dayStart(ts int64) int64 { return (ts / 86400) * 86400 }
