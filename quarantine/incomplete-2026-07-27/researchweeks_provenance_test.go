package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestProvenanceMatchesObservations pins the PUBLISHED survivorship measure to
// the ENFORCED one. ResearchObservations flags a row reconstructed when the
// symbol's added_at postdates THAT row's week; the era gate consumes that flag.
// ResearchUniverseProvenance used to judge each symbol against its era's LAST
// week instead, which forgives every symbol adopted mid-era — a strictly more
// lenient estimator of the same control, published while the strict one was
// enforced. This test fails against that older query.
func TestProvenanceMatchesObservations(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	const w = int64(WeekBucketSecs)
	mk := func(sym string, addedAt int64) int64 {
		s, err := st.UpsertSymbol(ctx, sym, md.Stocks, sym)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.w.ExecContext(ctx,
			`UPDATE symbols SET added_at=? WHERE id=?`, addedAt, s.ID); err != nil {
			t.Fatal(err)
		}
		return s.ID
	}

	// LATE was adopted after week 2601 ended but before 2602 ended: reconstructed
	// for its 2600 and 2601 observations, point-in-time for 2602. The era's last
	// week is 2602, so the old era-last-week predicate scored it clean outright.
	late := mk("LATE", (2601+1)*w+10)
	early := mk("EARLY", 100)

	rows := []ResearchWeek{
		// 2600: LATE only — a WHOLLY reconstructed week.
		{SymbolID: late, Week: 2600, Ts: 2600*w + 4*86400, Vec: map[string]float64{"a": 1},
			FwdReturn: 0.01, Up: true, Era: "e1"},
		// 2601: mixed — one clean name, so not wholly reconstructed.
		{SymbolID: late, Week: 2601, Ts: 2601*w + 4*86400, Vec: map[string]float64{"a": 1},
			FwdReturn: 0.01, Up: true, Era: "e1"},
		{SymbolID: early, Week: 2601, Ts: 2601*w + 4*86400, Vec: map[string]float64{"a": 1},
			FwdReturn: -0.01, Up: false, Era: "e1"},
		// 2602: both clean.
		{SymbolID: late, Week: 2602, Ts: 2602*w + 4*86400, Vec: map[string]float64{"a": 1},
			FwdReturn: 0.02, Up: true, Era: "e1"},
		{SymbolID: early, Week: 2602, Ts: 2602*w + 4*86400, Vec: map[string]float64{"a": 1},
			FwdReturn: 0.02, Up: true, Era: "e1"},
		// A second, wholly reconstructed era.
		{SymbolID: late, Week: 2500, Ts: 2500*w + 4*86400, Vec: map[string]float64{"a": 1},
			FwdReturn: 0.03, Up: true, Era: "e0"},
	}
	if err := st.UpsertResearchWeeks(ctx, rows, 1000); err != nil {
		t.Fatal(err)
	}

	obs, err := st.ResearchObservations(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != len(rows) {
		t.Fatalf("obs = %d, want %d", len(obs), len(rows))
	}
	// Ground truth recomputed from the flag the gate actually reads.
	type agg struct {
		obs, recon      int
		weekN, weekReco map[int64]int
	}
	byEra := map[string]*agg{}
	for _, o := range obs {
		a := byEra[o.Era]
		if a == nil {
			a = &agg{weekN: map[int64]int{}, weekReco: map[int64]int{}}
			byEra[o.Era] = a
		}
		a.obs++
		a.weekN[o.Week]++
		if o.UniverseReconstructed {
			a.recon++
			a.weekReco[o.Week]++
		}
	}

	p, err := st.ResearchUniverseProvenance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Eras) != len(byEra) {
		t.Fatalf("eras = %d, want %d", len(p.Eras), len(byEra))
	}
	totObs, totRecon, totWeeks, totWholly := 0, 0, 0, 0
	for _, e := range p.Eras {
		a := byEra[e.Era]
		if a == nil {
			t.Fatalf("provenance reports unknown era %q", e.Era)
		}
		if e.Obs != a.obs || e.ObsReconstructed != a.recon {
			t.Fatalf("era %s: obs %d/%d, want %d/%d (provenance disagrees with the flag the gate reads)",
				e.Era, e.ObsReconstructed, e.Obs, a.recon, a.obs)
		}
		wholly := 0
		for wk, n := range a.weekN {
			if a.weekReco[wk] == n {
				wholly++
			}
		}
		if e.Weeks != len(a.weekN) || e.WeeksWhollyReconstructed != wholly {
			t.Fatalf("era %s: wholly-reconstructed weeks %d/%d, want %d/%d",
				e.Era, e.WeeksWhollyReconstructed, e.Weeks, wholly, len(a.weekN))
		}
		totObs += a.obs
		totRecon += a.recon
		totWeeks += len(a.weekN)
		totWholly += wholly
	}
	if got, want := p.ObsReconstructedFrac, float64(totRecon)/float64(totObs); got != want {
		t.Fatalf("corpus obs frac = %v, want %v", got, want)
	}
	if got, want := p.WeeksWhollyReconstructedFrac, float64(totWholly)/float64(totWeeks); got != want {
		t.Fatalf("corpus wholly-week frac = %v, want %v", got, want)
	}

	// The fixture is chosen so the deleted lenient estimator would have read
	// zero reconstruction on e1 while the gate sees two of its five rows.
	var e1 EraProvenance
	for _, e := range p.Eras {
		if e.Era == "e1" {
			e1 = e
		}
	}
	if e1.ObsReconstructed != 2 || e1.Obs != 5 {
		t.Fatalf("e1 obs recon = %d/%d, want 2/5", e1.ObsReconstructed, e1.Obs)
	}
	if e1.WeeksWhollyReconstructed != 1 || e1.Weeks != 3 {
		t.Fatalf("e1 wholly weeks = %d/%d, want 1/3", e1.WeeksWhollyReconstructed, e1.Weeks)
	}
	// e0's single week is wholly reconstructed, so the era cannot acquit; the
	// era headline is derived from that week measure, not from a symbol share.
	if p.ErasReconstructedFrac != 0.5 {
		t.Fatalf("eras wholly reconstructed = %v, want 0.5", p.ErasReconstructedFrac)
	}
}
