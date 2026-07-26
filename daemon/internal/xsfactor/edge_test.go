package xsfactor

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// derivation mirrors the JSON that tools/xsfactor_edge.py writes. Only the
// fields the constants are checked against are decoded — an unknown field is
// not an error, so the script may grow diagnostics without breaking the build.
type derivation struct {
	Universe      string `json:"universe"`
	ForwardGuard  bool   `json:"forwardGuard"`
	Bootstrap     int    `json:"bootstrap"`
	Seed          int    `json:"seed"`
	SymbolsLoaded int    `json:"symbolsLoaded"`
	Script        string `json:"script"`
	Horizons      map[string]struct {
		Legs map[string]*struct {
			N               int     `json:"n"`
			DistinctDays    int     `json:"distinctDays"`
			EdgePP          float64 `json:"edgePP"`
			CILow           float64 `json:"ciLow"`
			CIHigh          float64 `json:"ciHigh"`
			CILowBonf       float64 `json:"ciLowBonferroni"`
			CIHighBonf      float64 `json:"ciHighBonferroni"`
			Survives95      bool    `json:"survives95"`
			SurvivesBonferr bool    `json:"survivesBonferroni"`
		} `json:"legs"`
	} `json:"horizons"`
}

func loadDerivation(t *testing.T) derivation {
	t.Helper()
	b, err := os.ReadFile(DerivationJSON)
	if err != nil {
		t.Fatalf("read %s: %v — the committed derivation is what makes these "+
			"constants auditable; without it they are assertions", DerivationJSON, err)
	}
	var d derivation
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("parse %s: %v", DerivationJSON, err)
	}
	return d
}

// floor2/ceil2 round a CI bound OUTWARD to 2dp. Rounding to nearest could shave
// a hundredth off an interval and make a claim marginally stronger than what was
// measured; rounding outward can only ever make it weaker.
func floor2(v float64) float64 { return math.Floor(v*100) / 100 }
func ceil2(v float64) float64  { return math.Ceil(v*100) / 100 }

// TestConstantsAreRederivable is the test finding H2 needed and did not have.
//
// The failure it prevents: a leg ships an edge that the platform's own
// derivation contradicts — which is exactly what happened to LIQUIDITY, shipped
// at +2.50pp (21d) / +3.04pp (63d) while every honest re-derivation of it comes
// back NEGATIVE. Before this test the constants came from a script that was
// never committed, so nothing in the build could notice.
//
// Every published constant must equal the committed output of
// tools/xsfactor_edge.py, rounded to 2dp with the interval rounded outward, and
// no leg whose measured interval touches or crosses zero may be published at
// all.
func TestConstantsAreRederivable(t *testing.T) {
	d := loadDerivation(t)

	if d.Script == "" || d.Script != DerivationScript {
		t.Fatalf("derivation.json says script=%q, constants name %q — the payload "+
			"would point a reader at the wrong file", d.Script, DerivationScript)
	}
	// The script must actually be there. A named-but-absent derivation is the
	// same unauditable state H2 was raised about.
	if _, err := os.Stat(filepath.Join("..", "..", "..", DerivationScript)); err != nil {
		t.Fatalf("stat %s: %v", DerivationScript, err)
	}

	for _, h := range Horizons {
		hd, ok := d.Horizons[string(h)]
		if !ok {
			t.Fatalf("%s: no derivation — a shipped horizon with no re-derivation "+
				"is the H2 defect all over again", h)
		}
		for _, l := range MeasuredEdge(h) {
			m := hd.Legs[l.Leg]
			if m == nil {
				t.Fatalf("%s/%s is published but the derivation never measured it", h, l.Leg)
			}
			// A published edge is a claim that the effect is real and POSITIVE.
			// The derivation is the only thing entitled to settle that.
			if !m.Survives95 || m.EdgePP <= 0 || m.CILow <= 0 {
				t.Fatalf("%s/%s is PUBLISHED as +%.2fpp but the derivation measures "+
					"%+.2fpp CI [%+.2f, %+.2f] — a published leg must have a "+
					"strictly positive interval", h, l.Leg, l.EdgePP,
					m.EdgePP, m.CILow, m.CIHigh)
			}
			if l.EdgePP != math.Round(m.EdgePP*100)/100 {
				t.Fatalf("%s/%s ships edge %+.2fpp, derivation says %+.4fpp",
					h, l.Leg, l.EdgePP, m.EdgePP)
			}
			if l.CILow != floor2(m.CILow) || l.CIHigh != ceil2(m.CIHigh) {
				t.Fatalf("%s/%s ships CI [%+.2f, %+.2f], derivation says "+
					"[%+.4f, %+.4f] (want [%+.2f, %+.2f] rounded outward)",
					h, l.Leg, l.CILow, l.CIHigh, m.CILow, m.CIHigh,
					floor2(m.CILow), ceil2(m.CIHigh))
			}
		}
		// EVERY leg the derivation measured must be accounted for in the shipped
		// block, published or withheld with a reason. A leg that is measured and
		// then simply not mentioned is how a bad result disappears.
		shipped := map[string]LegEdge{}
		for _, l := range AllMeasured(h) {
			if _, dup := shipped[l.Leg]; dup {
				t.Fatalf("%s/%s appears twice in the block", h, l.Leg)
			}
			shipped[l.Leg] = l
		}
		for leg, m := range hd.Legs {
			// The derivation also reports composite diagnostics; only the three
			// real legs ship as constants.
			if m == nil || (leg != LegLiquidity && leg != LegLowVol && leg != LegMom121) {
				continue
			}
			l, ok := shipped[leg]
			if !ok {
				t.Fatalf("%s/%s was measured (%+.2fpp) but does not ship at all",
					h, leg, m.EdgePP)
			}
			// Status must agree with the interval, in both directions.
			if got, want := l.Status == StatusPublished, m.Survives95 && m.EdgePP > 0; got != want {
				t.Fatalf("%s/%s status=%q but the derivation says survives95=%v edge=%+.2fpp",
					h, leg, l.Status, m.Survives95, m.EdgePP)
			}
			if l.Status != StatusPublished && l.Reason == "" {
				t.Fatalf("%s/%s is not published and states no reason", h, leg)
			}
			if l.Positive() != (l.Status == StatusPublished) {
				t.Fatalf("%s/%s: Positive()=%v disagrees with status %q",
					h, leg, l.Positive(), l.Status)
			}
			// The measured values ship for withheld legs too — a retracted leg
			// with its number stripped out is not a retraction, it is a deletion.
			if l.EdgePP != math.Round(m.EdgePP*100)/100 ||
				l.CILow != floor2(m.CILow) || l.CIHigh != ceil2(m.CIHigh) {
				t.Fatalf("%s/%s ships %+.2fpp [%+.2f, %+.2f], derivation says "+
					"%+.4fpp [%+.4f, %+.4f]", h, leg, l.EdgePP, l.CILow, l.CIHigh,
					m.EdgePP, m.CILow, m.CIHigh)
			}
			if l.CILowBonferroni != floor2(m.CILowBonf) || l.CIHighBonferroni != ceil2(m.CIHighBonf) {
				t.Fatalf("%s/%s ships Bonferroni [%+.2f, %+.2f], derivation says "+
					"[%+.4f, %+.4f]", h, leg, l.CILowBonferroni, l.CIHighBonferroni,
					m.CILowBonf, m.CIHighBonf)
			}
			// The corrected interval can only ever be wider than the raw one.
			if l.CILowBonferroni > l.CILow || l.CIHighBonferroni < l.CIHigh {
				t.Fatalf("%s/%s Bonferroni interval [%+.2f, %+.2f] is narrower than "+
					"the 95%% one [%+.2f, %+.2f]", h, leg, l.CILowBonferroni,
					l.CIHighBonferroni, l.CILow, l.CIHigh)
			}
			if l.N != m.N || l.DistinctDays != m.DistinctDays {
				t.Fatalf("%s/%s ships n=%d days=%d, derivation says n=%d days=%d",
					h, leg, l.N, l.DistinctDays, m.N, m.DistinctDays)
			}
			// The house invariant, enforced where it is easiest to violate:
			// the interval resamples DAYS, and there are always fewer of them.
			if l.DistinctDays >= l.N {
				t.Fatalf("%s/%s claims %d distinct days from %d observations",
					h, leg, l.DistinctDays, l.N)
			}
		}
	}
}

// TestDerivationRanWhatItClaims guards the method itself. The numbers are only
// worth anything if the run that produced them used the stated universe and the
// stated resampling unit; a derivation quietly re-run with the forward guard off
// or on survivors only would move every constant in this file.
func TestDerivationRanWhatItClaims(t *testing.T) {
	d := loadDerivation(t)
	if d.Universe != "all" {
		t.Fatalf("derivation universe=%q, want the survivorship-clean \"all\" — "+
			"measuring factors on 2026 survivors only is the bias the delisted "+
			"bars were kept to avoid", d.Universe)
	}
	if !d.ForwardGuard {
		t.Fatal("derivation ran with the forward split guard OFF; an uncorrected " +
			"split inside a forward window is not a return")
	}
	if d.Bootstrap < 20000 {
		t.Fatalf("bootstrap=%d — the Bonferroni tail needs enough resamples to "+
			"be more than one draw", d.Bootstrap)
	}
	if d.SymbolsLoaded < 900 {
		t.Fatalf("symbolsLoaded=%d, far below the ~1,059-name universe the "+
			"constants claim", d.SymbolsLoaded)
	}
}

// TestLiquidityLegIsRetracted pins finding H2 itself.
//
// The failure it prevents: the size/liquidity leg silently coming back. Its
// measured edge is NEGATIVE at all three horizons (-1.10 / -1.65 / -1.92pp) and
// it is the only leg in the whole block that survives Bonferroni — in the wrong
// direction. It must not be published and must not be composited, at any
// horizon, whatever a future edit believes about the size premium.
func TestLiquidityLegIsRetracted(t *testing.T) {
	d := loadDerivation(t)
	for _, h := range Horizons {
		for _, l := range MeasuredEdge(h) {
			if l.Leg == LegLiquidity {
				t.Fatalf("%s: liquidity is published at %+.2fpp — the derivation "+
					"measures %+.4fpp", h, l.EdgePP, d.Horizons[string(h)].Legs[LegLiquidity].EdgePP)
			}
		}
		for _, leg := range CompositeLegs(h) {
			if leg == LegLiquidity {
				t.Fatalf("%s: liquidity is still averaged into the composite", h)
			}
		}
	}
}
