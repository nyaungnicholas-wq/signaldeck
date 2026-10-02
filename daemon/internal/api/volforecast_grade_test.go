package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volprereg"
)

// seedRVStudy writes a registration at regTs, then for 30 symbols a daily bar
// series whose squared close-to-close return on day i is v(i,s), and resolved
// forecasts on `days` call days frozen at createdTs. HAR is 5% off the
// outcome, EWMA 100%, so every reading of the rule says BEATS THE NULLS once
// the floor is met.
func seedRVStudy(t *testing.T, st *store.Store, regTs, createdTs, t0 int64, days int) {
	t.Helper()
	ctx := context.Background()
	if regTs > 0 {
		if _, err := st.AppendPrereg(ctx, prereg.Record{Kind: volprereg.RVForecastKind, Ts: regTs,
			SpecHash: "hash-of-the-json", Note: "test",
			SpecJSON: `{"specDigest":"` + volprereg.Registration().Hash() + `"}`}); err != nil {
			t.Fatal(err)
		}
	}
	v := func(i, s int) float64 { return 1e-4 * (1 + 0.3*math.Sin(float64(7*i+3*s))) }
	for s := 0; s < 30; s++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("RV%02d", s), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		n := days + 6
		bars := make([]md.Bar, n)
		c := 100.0
		for i := range bars {
			if i > 0 {
				r := math.Sqrt(v(i, s))
				if i%2 == 0 {
					r = -r
				}
				c *= math.Exp(r)
			}
			bars[i] = md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: t0 + int64(i)*86400,
				Open: c, High: c * 1.01, Low: c * 0.99, Close: c, Volume: 1e6}
		}
		if err := st.UpsertBars(ctx, bars); err != nil {
			t.Fatal(err)
		}
		for _, h := range []int{1, 5} {
			for i := 0; i < days; i++ {
				out := 0.0
				for k := i + 1; k <= i+h; k++ {
					out += v(k, s)
				}
				out /= float64(h)
				f := store.RVForecast{SymbolID: sym.ID, Ts: bars[i].Ts, Horizon: h,
					RVHat: out * 1.05, NullRW: out * 1.8, NullEWMA: out * 2, Revision: "test"}
				if _, err := st.FreezeRVForecast(ctx, f, time.Unix(createdTs, 0)); err != nil {
					t.Fatal(err)
				}
				if err := st.ResolveRVForecast(ctx, sym.ID, bars[i].Ts, h, out, time.Unix(createdTs+1, 0)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

type gradedHorizon struct {
	Horizon       int             `json:"horizon"`
	DistinctDays  int             `json:"distinctDays"`
	Verdict       string          `json:"verdict"`
	VerdictReason string          `json:"verdictReason"`
	Grade         json.RawMessage `json:"grade"`
	Readings      []any           `json:"readings"`
}

func readRVRecord(t *testing.T, d Deps) map[int]gradedHorizon {
	t.Helper()
	rr := httptest.NewRecorder()
	d.volForecastRecord(rr, httptest.NewRequest("GET", "/api/vol-forecast/record", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Horizons []gradedHorizon `json:"horizons"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := map[int]gradedHorizon{}
	for _, h := range body.Horizons {
		out[h.Horizon] = h
	}
	return out
}

func TestVolRecordGradesTheHeadlineAtTheFloor(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	reg := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()
	seedRVStudy(t, st, reg, reg+60, reg+86400, 64)

	got := readRVRecord(t, d)
	h1, h5 := got[1], got[5]
	if h1.DistinctDays != 64 || h1.Verdict != "BEATS THE NULLS" {
		t.Fatalf("h1: %d days, verdict %q (%s)", h1.DistinctDays, h1.Verdict, h1.VerdictReason)
	}
	if len(h1.Grade) == 0 || len(h1.Readings) != 18 {
		t.Fatalf("h1 must carry its statistics and 18 readings: grade %s, %d readings", h1.Grade, len(h1.Readings))
	}
	if h5.Verdict != rvSecondary || len(h5.Readings) != 0 {
		t.Fatalf("h5 is secondary and cannot carry a verdict: %q, %d readings", h5.Verdict, len(h5.Readings))
	}
}

// A registration whose digest is not the compiled spec is never graded.
func TestVolRecordRefusesADriftedSpec(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	reg := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()
	seedRVStudy(t, st, reg, reg+60, reg+86400, 64)
	if _, err := st.AppendPrereg(context.Background(), prereg.Record{Kind: volprereg.RVForecastKind,
		Ts: reg + 1, SpecHash: "x", Note: "amended", SpecJSON: `{"specDigest":"not-this-spec"}`}); err != nil {
		t.Fatal(err)
	}
	if h1 := readRVRecord(t, d)[1]; h1.Verdict != "ACCRUING" {
		t.Fatalf("drifted spec graded anyway: %q (%s)", h1.Verdict, h1.VerdictReason)
	}
}

// Below the floor: INSUFFICIENT, and no test statistic is published at all.
// Forecasts frozen at or before the registration are outside the live window
// (the start rule), so 50 pre-registration days cannot lift 20 live ones.
func TestVolRecordFloorAndStartRule(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	reg := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()
	seedRVStudy(t, st, reg, reg, reg-90*86400, 50) // frozen AT the registration second: excluded
	seedRVStudy(t, st, 0, reg+60, reg+86400, 20)

	for h, r := range readRVRecord(t, d) {
		if r.Verdict != "INSUFFICIENT" || len(r.Grade) != 0 || len(r.Readings) != 0 {
			t.Fatalf("h%d: verdict %q, grade %s, %d readings; want INSUFFICIENT with nothing published",
				h, r.Verdict, r.Grade, len(r.Readings))
		}
		if h == 1 && r.DistinctDays != 20 {
			t.Fatalf("h1 counted %d days, want the 20 live ones", r.DistinctDays)
		}
	}
}
