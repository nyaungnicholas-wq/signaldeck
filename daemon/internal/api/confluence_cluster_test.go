// C4 (residual) — /api/confluence/track publishes a MONEY scoreboard (win rate,
// expectancy, profit factor) over observations that are one per (symbol, UTC-day)
// and therefore massively day-clustered, with no interval of any kind beside them.
//
// Measured on the live record (2026-07-25, data/signaldeck.db), over exactly the
// rows this endpoint reads and with the same 0.1%/side cost netted:
//
//	N = 2,530 independent obs over 11 distinct days, win rate 40.51%
//	naive Wilson 95%  : [0.3862, 0.4244]   width 0.0382
//	measured deff     : 27.25  ->  effective N 92.8, not 2,530
//	cluster Wilson 95%: [0.3110, 0.5068]   width 0.1959   (5.12x wider)
//
// And the headline expectancy of −4.36% is one day: the per-day expectancies run
// +0.41%, +1.23%, −22.58%, −1.01%, −1.13%, −5.34%, −2.27%, +0.42%, +0.33%,
// −0.43%, −0.63%. A row-level standard error calls that −4.36% ±3.4pp. Resampling
// DAYS — the unit that actually varies — does not.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newConfluenceServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "conf.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{
		St:      st,
		Cfg:     config.Config{WebOrigins: []string{"http://app.example"}, PublicReads: true},
		Version: "test",
		Started: time.Now(),
	}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	mux := http.NewServeMux()
	d.registerConfluence(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

// seedConfluenceTrack writes resolved LONG setups: perDay symbols on each of
// days distinct UTC days, all of a day sharing that day's realized move. That is
// the real shape of this record (every setup on a day rides the same tape), and
// it is what makes 2,530 rows behave like 11.
func seedConfluenceTrack(t *testing.T, st *store.Store, dayReturns []float64, perDay int) {
	t.Helper()
	ctx := context.Background()
	syms := make([]int64, perDay)
	for i := range syms {
		s, err := st.UpsertSymbol(ctx, "CF"+string(rune('A'+i/26))+string(rune('A'+i%26)), md.Stocks, "")
		if err != nil {
			t.Fatalf("upsert symbol: %v", err)
		}
		syms[i] = s.ID
	}
	for day, ret := range dayReturns {
		base := int64(20000+day)*86400 + 43200
		for i, id := range syms {
			ts := base + int64(i)
			if err := st.InsertConfluenceOutcome(ctx, store.ConfluenceOutcome{
				SymbolID: id, Ts: ts, Horizon: "1d", Direction: 1, Agree: 4, EntryPx: 100,
			}); err != nil {
				t.Fatalf("insert outcome: %v", err)
			}
			// A small per-symbol wobble around the day's move so the sample is not
			// literally degenerate, but the DAY dominates — as it does live.
			r := ret + float64(i%5-2)*0.0005
			if err := st.ResolveConfluenceOutcome(ctx, id, ts, "1d", r, r > 0); err != nil {
				t.Fatalf("resolve outcome: %v", err)
			}
		}
	}
}

func confluenceTrackBody(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	var body map[string]any
	if code := s8Get(t, srv.URL+"/api/confluence/track", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	return body
}

// TestConfluenceTrack_ReportsMeasuredClustering is the failing test: the money
// scoreboard ships a win rate and an expectancy over thousands of rows without
// ever saying how many DAYS they came from or how much the day clustering
// inflates their variance. Raw N is not a sample size.
func TestConfluenceTrack_ReportsMeasuredClustering(t *testing.T) {
	srv, st := newConfluenceServer(t)
	// 12 days, alternating winners and losers, one big loss day — the live shape.
	seedConfluenceTrack(t, st, []float64{
		0.012, -0.010, 0.008, -0.045, 0.011, -0.009,
		0.013, -0.011, 0.009, -0.008, 0.010, -0.012,
	}, 40)

	body := confluenceTrackBody(t, srv)
	raw, _ := json.Marshal(body["cluster"])
	var cl struct {
		N                int     `json:"n"`
		DistinctDays     int     `json:"distinctDays"`
		DesignEffect     float64 `json:"designEffect"`
		EffectiveN       float64 `json:"effectiveN"`
		CI               *[2]any `json:"-"`
		Refused          bool    `json:"refused"`
		Reason           string  `json:"reason"`
		WidthRatio       float64 `json:"widthRatio"`
		CIRaw            *ciJSON `json:"ci"`
		NaiveDiscredited *ciJSON `json:"naiveCIDiscredited"`
	}
	if err := json.Unmarshal(raw, &cl); err != nil {
		t.Fatalf("no cluster block on the money scoreboard: %v (payload keys: %v)", err, keysOf(body))
	}
	if cl.N != 480 || cl.DistinctDays != 12 {
		t.Fatalf("N/days = %d/%d, want 480/12", cl.N, cl.DistinctDays)
	}
	if cl.DesignEffect <= 1.5 {
		t.Fatalf("design effect = %.2f — the day clustering in this fixture was not measured", cl.DesignEffect)
	}
	if cl.EffectiveN >= float64(cl.N) {
		t.Fatalf("effective N %.1f >= raw N %d", cl.EffectiveN, cl.N)
	}
	if cl.CIRaw == nil || cl.NaiveDiscredited == nil {
		t.Fatal("at 12 distinct days both the corrected and the discredited naive interval must be present")
	}
	if cl.CIRaw.width() <= cl.NaiveDiscredited.width() {
		t.Fatalf("corrected width %.4f not wider than naive %.4f", cl.CIRaw.width(), cl.NaiveDiscredited.width())
	}
}

// TestConfluenceTrack_ExpectancyIntervalResamplesDays: the headline number on
// this surface is expectancy, not win rate, so it needs an interval too — and it
// must come from resampling WHOLE DAYS. A row-level standard error over 480 rows
// whose variance lives entirely between 12 days is a fiction; the honest interval
// must be wide enough to contain zero on a record this thin.
func TestConfluenceTrack_ExpectancyIntervalResamplesDays(t *testing.T) {
	srv, st := newConfluenceServer(t)
	seedConfluenceTrack(t, st, []float64{
		0.012, -0.010, 0.008, -0.045, 0.011, -0.009,
		0.013, -0.011, 0.009, -0.008, 0.010, -0.012,
	}, 40)

	body := confluenceTrackBody(t, srv)
	raw, _ := json.Marshal(body["expectancy"])
	var exp struct {
		Point   float64 `json:"point"`
		CI      *ciJSON `json:"ci"`
		Refused bool    `json:"refused"`
		Reason  string  `json:"reason"`
	}
	if err := json.Unmarshal(raw, &exp); err != nil || body["expectancy"] == nil {
		t.Fatalf("no day-resampled expectancy interval on the money scoreboard (payload keys: %v)", keysOf(body))
	}
	if exp.CI == nil {
		t.Fatalf("expectancy interval withheld at 12 days: %s", exp.Reason)
	}
	if !(exp.CI.Lo < exp.Point && exp.Point < exp.CI.Hi) {
		t.Fatalf("point %.5f outside its own interval [%.5f, %.5f]", exp.Point, exp.CI.Lo, exp.CI.Hi)
	}
	// One day of −4.5% carries the whole mean here. Resampling days must show
	// that: the interval has to straddle zero, which a row-level SE does not.
	if !(exp.CI.Lo < 0 && exp.CI.Hi > 0) {
		t.Fatalf("day-resampled expectancy interval [%.5f, %.5f] excludes zero on a 12-day record dominated by one day",
			exp.CI.Lo, exp.CI.Hi)
	}
}

// TestConfluenceTrack_ThinDirectionWithheld: the long/short split is a SEPARATE
// sample and must clear the day floor on its own. Here every short lands inside
// three days, so the short book gets no interval and a stated reason — never a
// narrow band inherited from the pooled record's day count.
func TestConfluenceTrack_ThinDirectionWithheld(t *testing.T) {
	srv, st := newConfluenceServer(t)
	seedConfluenceTrack(t, st, []float64{
		0.012, -0.010, 0.008, -0.045, 0.011, -0.009,
		0.013, -0.011, 0.009, -0.008, 0.010, -0.012,
	}, 40)
	// Shorts on three days only.
	ctx := context.Background()
	sh, err := st.UpsertSymbol(ctx, "SHORTY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	for day := 0; day < 3; day++ {
		for i := 0; i < 12; i++ {
			ts := int64(20000+day)*86400 + 3600 + int64(i)
			if err := st.InsertConfluenceOutcome(ctx, store.ConfluenceOutcome{
				SymbolID: sh.ID, Ts: ts, Horizon: "1d", Direction: -1, Agree: 4, EntryPx: 50,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.ResolveConfluenceOutcome(ctx, sh.ID, ts, "1d", -0.02, true); err != nil {
				t.Fatal(err)
			}
		}
	}

	body := confluenceTrackBody(t, srv)
	byDir, ok := body["byDirection"].(map[string]any)
	if !ok {
		t.Fatalf("byDirection missing: %v", keysOf(body))
	}
	short, ok := byDir["short"].(map[string]any)
	if !ok {
		t.Fatalf("byDirection.short missing: %v", byDir)
	}
	raw, _ := json.Marshal(short["cluster"])
	var cl struct {
		DistinctDays int     `json:"distinctDays"`
		Refused      bool    `json:"refused"`
		Reason       string  `json:"reason"`
		CI           *ciJSON `json:"ci"`
	}
	if err := json.Unmarshal(raw, &cl); err != nil || short["cluster"] == nil {
		t.Fatalf("short book has no cluster block: %v", short)
	}
	if cl.CI != nil {
		t.Fatalf("short book published an interval off %d distinct days", cl.DistinctDays)
	}
	if !cl.Refused || cl.Reason == "" {
		t.Fatalf("withheld short interval carries no reason: %+v", cl)
	}
}

// ciJSON decodes a clusterstat.Interval out of a payload.
type ciJSON struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

func (c ciJSON) width() float64 { return c.Hi - c.Lo }

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
