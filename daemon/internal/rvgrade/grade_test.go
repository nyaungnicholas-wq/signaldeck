package rvgrade

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// panel builds nDays x nSyms rows. f(day, sym) returns the row's numbers.
func panel(nDays, nSyms int, f func(d, s int) Row) []Row {
	var out []Row
	t0 := time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC)
	for d := 0; d < nDays; d++ {
		day := t0.AddDate(0, 0, d).Format("2006-01-02")
		for s := 0; s < nSyms; s++ {
			r := f(d, s)
			r.Day = day
			out = append(out, r)
		}
	}
	return out
}

// wobble is a deterministic, non-constant day-and-symbol factor so every
// daily differential series has positive variance.
func wobble(d, s int) float64 { return 1 + 0.3*math.Sin(float64(7*d+3*s)) }

// HAR tracks the outcome, EWMA is twice it: HAR wins QLIKE on RV^GK, and with
// RV^CC equal to RV^GK it wins the control too.
func beatsRows(n int) []Row {
	return panel(n, 30, func(d, s int) Row {
		gk := 1e-4 * wobble(d, s)
		return Row{HAR: gk * 1.05, RW: gk * 1.8, EWMA: gk * 2, GK: gk, CC: gk}
	})
}

func TestOutcomeBeatsTheNulls(t *testing.T) {
	r := Decide(GradeHorizon(beatsRows(70), 1))
	if r.Verdict != Beats {
		t.Fatalf("verdict %q (%s), want %q", r.Verdict, r.Reason, Beats)
	}
	if len(r.Readings) != 18 {
		t.Fatalf("%d readings, want 3 divisors x 2 distributions x 3 control bars", len(r.Readings))
	}
}

// Same headline, but RV^CC sits on the EWMA forecast: the advantage exists
// only on the range proxy.
func TestOutcomeEstimatorArtifact(t *testing.T) {
	rows := beatsRows(70)
	for i := range rows {
		rows[i].CC = rows[i].EWMA * (1 + 0.01*math.Cos(float64(i)))
	}
	r := Decide(GradeHorizon(rows, 1))
	if r.Verdict != Artifact {
		t.Fatalf("verdict %q (%s), want %q", r.Verdict, r.Reason, Artifact)
	}
}

// HAR and EWMA trade places day by day: the mean differential is exactly zero.
func TestOutcomeNoSkillDemonstrated(t *testing.T) {
	rows := panel(70, 30, func(d, s int) Row {
		gk := 1e-4 * wobble(d, s)
		a, b := gk*1.5, gk*0.8
		if d%2 == 1 {
			a, b = b, a
		}
		return Row{HAR: a, RW: gk, EWMA: b, GK: gk, CC: gk}
	})
	g := GradeHorizon(rows, 1)
	r := Decide(g)
	if r.Verdict != NoSkill {
		t.Fatalf("verdict %q (%s), want %q (headline %+v)", r.Verdict, r.Reason, NoSkill, g.Headline)
	}
}

// The evidence floor: 59 counted days is INSUFFICIENT, 60 rules. A day with
// 29 symbols is not counted at all, however many other days there are.
func TestOutcomeInsufficientAndFloor(t *testing.T) {
	if r := Decide(GradeHorizon(beatsRows(59), 1)); r.Verdict != Insufficient {
		t.Fatalf("59 days: verdict %q, want %q", r.Verdict, Insufficient)
	}
	if r := Decide(GradeHorizon(beatsRows(60), 1)); r.Verdict != Beats {
		t.Fatalf("60 days: verdict %q, want %q", r.Verdict, Beats)
	}

	thin := append(beatsRows(59), panel(1, 29, func(d, s int) Row {
		return Row{HAR: 1, RW: 1, EWMA: 2, GK: 1, CC: 1}
	})...)
	for i := len(thin) - 29; i < len(thin); i++ {
		thin[i].Day = "2027-01-01"
	}
	g := GradeHorizon(thin, 1)
	if g.Headline.Days != 59 || g.Headline.ThinDays != 1 {
		t.Fatalf("days %d thin %d, want 59 and 1", g.Headline.Days, g.Headline.ThinDays)
	}
	if r := Decide(g); r.Verdict != Insufficient {
		t.Fatalf("59 full days + one 29-symbol day: verdict %q, want %q", r.Verdict, Insufficient)
	}
}

// cellAt makes a cell whose statistics are given directly, so the decision
// map can be tested at exact p-values.
func cellAt(mean, tstat float64, days int) Cell {
	return Cell{Days: days, OK: true, Mean: mean, T: tstat,
		PStudent: StudentTTwoSidedP(tstat, days-1), PNormal: math.Erfc(math.Abs(tstat) / math.Sqrt2)}
}

func TestOpenReadingsHoldInsteadOfRuling(t *testing.T) {
	strong := cellAt(-0.05, -8, 80)

	// Control favours HAR by sign but is nowhere near significant: the
	// "sign" reading says BEATS, the other two say ARTIFACT.
	r := Decide(Grade{Headline: strong, Control: cellAt(-1e-9, -0.5, 80)})
	if r.Verdict != Accruing || !strings.Contains(r.Reason, Beats) || !strings.Contains(r.Reason, Artifact) {
		t.Fatalf("split control: verdict %q (%s), want ACCRUING naming both readings", r.Verdict, r.Reason)
	}

	// Headline t = -3.2 at 80 days: Student-t p ~ 0.0020, normal p ~ 0.0014.
	// Under 0.05/26 = 0.00192 the normal reading clears and the Student-t one
	// does not, so the divisor/distribution readings disagree.
	r = Decide(Grade{Headline: cellAt(-0.01, -3.2, 80), Control: strong})
	if r.Verdict != Accruing || !strings.Contains(r.Reason, NoSkill) {
		t.Fatalf("borderline headline: verdict %q (%s), want ACCRUING", r.Verdict, r.Reason)
	}

	// Significant in EWMA's favour: the registered rule names no outcome.
	r = Decide(Grade{Headline: cellAt(+0.05, +8, 80), Control: strong})
	if r.Verdict != Accruing || !strings.Contains(r.Reason, "UNREGISTERED") {
		t.Fatalf("wrong-sign headline: verdict %q (%s), want ACCRUING naming the gap", r.Verdict, r.Reason)
	}

	// An undefined headline statistic is never read as a verdict.
	r = Decide(Grade{Headline: Cell{Days: 80}, Control: strong})
	if r.Verdict != Accruing {
		t.Fatalf("undefined headline: verdict %q, want ACCRUING", r.Verdict)
	}
}

func TestDivisorsReadFromTheSpec(t *testing.T) {
	got := Divisors()
	if len(got) != 3 || got[0] != 26 || got[1] != 48 || got[2] != 72 {
		t.Fatalf("divisors %v, want [26 48 72] from FamilySize 24 and LooksSpent 2", got)
	}
}

func TestCCTarget(t *testing.T) {
	bars := []md.Bar{{Ts: 1, Close: 100}, {Ts: 2, Close: 110}, {Ts: 3, Close: 99}, {Ts: 4, Close: 0}}
	l1, l2 := math.Log(1.1), math.Log(99.0/110)
	if got := CCTarget(bars, 1, 1); math.Abs(got-l1*l1) > 1e-15 {
		t.Fatalf("h1 = %v, want %v", got, l1*l1)
	}
	if got := CCTarget(bars, 1, 2); math.Abs(got-(l1*l1+l2*l2)/2) > 1e-15 {
		t.Fatalf("h2 = %v", got)
	}
	for _, c := range []struct {
		ts int64
		h  int
	}{{3, 1}, {4, 1}, {9, 1}, {2, 5}} { // zero close, past the end, absent bar, partial window
		if got := CCTarget(bars, c.ts, c.h); !math.IsNaN(got) {
			t.Errorf("CCTarget(ts %d, h %d) = %v, want NaN", c.ts, c.h, got)
		}
	}
}

// TestParityWithPythonReference grades the fixture written by
// tools/rv_grader_parity.py and compares every shared cell with what
// tools/rv_forecast_backtest.py computed on the same rows.
func TestParityWithPythonReference(t *testing.T) {
	var fx struct {
		Rows []struct {
			Ts                int64
			Har, Rw, Ewma, Gk float64
			Cc                *float64
		} `json:"rows"`
	}
	type stat struct {
		Mean, T float64
		N, Lag  int
		Zero    int
	}
	var exp struct {
		Horizons map[string]struct {
			Headline stat `json:"headline"`
			QlikeRW  stat `json:"qlike_rw"`
			MseEwma  stat `json:"mse_ewma"`
			Control  stat `json:"control"`
		} `json:"horizons"`
		CC map[string]struct {
			Closes  []float64             `json:"closes"`
			Targets map[string][]*float64 `json:"targets"`
		} `json:"cc"`
	}
	for path, v := range map[string]any{"testdata/parity_rows.json": &fx, "testdata/parity_expected.json": &exp} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	if len(fx.Rows) == 0 || len(exp.Horizons) != 2 || len(exp.CC) != 3 {
		t.Fatalf("fixture incomplete: %d rows, %d horizons, %d cc series", len(fx.Rows), len(exp.Horizons), len(exp.CC))
	}
	rows := make([]Row, len(fx.Rows))
	for i, r := range fx.Rows {
		cc := math.NaN()
		if r.Cc != nil {
			cc = *r.Cc
		}
		rows[i] = Row{Day: time.Unix(r.Ts, 0).UTC().Format("2006-01-02"),
			HAR: r.Har, RW: r.Rw, EWMA: r.Ewma, GK: r.Gk, CC: cc}
	}

	approx := func(name string, got, want float64) {
		t.Helper()
		if d := math.Abs(got - want); d > 1e-9*math.Max(1, math.Abs(want)) && d > 1e-15 {
			t.Errorf("%s: Go %.17g, Python %.17g", name, got, want)
		}
	}
	checked := 0
	for _, h := range []int{1, 5} {
		e := exp.Horizons[map[int]string{1: "1", 5: "5"}[h]]
		g := GradeHorizon(rows, h)
		for _, c := range []struct {
			name string
			got  Cell
			want stat
		}{
			{"headline", g.Headline, e.Headline},
			{"qlike vs rw", g.Secondary[0], e.QlikeRW},
			{"mse vs ewma", g.Secondary[1], e.MseEwma},
			{"control", g.Control, e.Control},
		} {
			if !c.got.OK || c.got.Days != c.want.N || c.got.Lag != c.want.Lag {
				t.Errorf("h%d %s: ok %v days %d lag %d, Python n %d lag %d",
					h, c.name, c.got.OK, c.got.Days, c.got.Lag, c.want.N, c.want.Lag)
			}
			approx(c.name+" mean", c.got.Mean, c.want.Mean)
			approx(c.name+" t", c.got.T, c.want.T)
			checked++
		}
		if g.CCZero != e.Control.Zero {
			t.Errorf("h%d zero-return exclusions: Go %d, Python %d", h, g.CCZero, e.Control.Zero)
		}
	}
	for name, s := range exp.CC {
		bars := make([]md.Bar, len(s.Closes))
		for i, c := range s.Closes {
			bars[i] = md.Bar{Ts: int64(i), Close: c}
		}
		for hs, want := range s.Targets {
			h := map[string]int{"1": 1, "5": 5}[hs]
			for i, w := range want {
				got := CCTarget(bars, int64(i), h)
				if w == nil != math.IsNaN(got) || (w != nil && math.Abs(got-*w) > 1e-15) {
					t.Errorf("cc %s h%d t%d: Go %v, Python %v", name, h, i, got, w)
				}
				checked++
			}
		}
	}
	if !t.Failed() {
		t.Logf("PARITY OK: %d comparisons against tools/rv_forecast_backtest.py", checked)
	}
}
