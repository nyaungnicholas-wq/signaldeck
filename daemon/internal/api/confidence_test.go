package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newConfidenceServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerConfidence(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

type confidenceResp struct {
	Note       string `json:"note"`
	Symbol     string `json:"symbol"`
	Horizon    string `json:"horizon"`
	Assessed   bool   `json:"assessed"`
	Reason     string `json:"reason"`
	HoldBars   int    `json:"holdBars"`
	Assessment struct {
		Probability      float64  `json:"probability"`
		ExpectedReturn   *float64 `json:"expectedReturn"`
		ExpectedDrawdown *float64 `json:"expectedDrawdown"`
		DrawdownTail     *float64 `json:"drawdownTail"`
		Confidence       *float64 `json:"confidence"`
		Uncertainty      *float64 `json:"uncertainty"`
		Reasons          []string `json:"reasons"`
		Withheld         []string `json:"withheld"`
	} `json:"assessment"`
	Excursion struct {
		N     int     `json:"n"`
		Mean  float64 `json:"mean"`
		P90   float64 `json:"p90"`
		Valid bool    `json:"valid"`
		Note  string  `json:"note"`
	} `json:"excursion"`
}

// No prediction stored → an honest "nothing to assess", not a zero-filled object.
func TestConfidenceWithoutAPredictionSaysSo(t *testing.T) {
	srv, st := newConfidenceServer(t)
	ctx := context.Background()
	if _, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var got confidenceResp
	if code := getFleetJSON(t, srv.URL+"/api/confidence?symbol=AAA&market=stocks&horizon=1d", &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	if got.Assessed {
		t.Error("with no stored prediction there is nothing to assess")
	}
	if got.Reason == "" {
		t.Error("the refusal must explain itself")
	}
}

// With a prediction but no history, the probability passes through and EVERY
// derived field is withheld and named. This is the core honesty contract.
func TestConfidenceWithheldOnAColdPlatform(t *testing.T) {
	srv, st := newConfidenceServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 86400,
		RawProb: 0.83, CalProb: 0.83, NUsed: 5, Components: "{}",
	}); err != nil {
		t.Fatalf("prediction: %v", err)
	}

	var got confidenceResp
	if code := getFleetJSON(t, srv.URL+"/api/confidence?symbol=AAA&market=stocks&horizon=1d", &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !got.Assessed {
		t.Fatalf("a stored prediction should be assessed: %s", got.Reason)
	}
	if got.Assessment.Probability != 0.83 {
		t.Errorf("probability should pass through unchanged, got %v", got.Assessment.Probability)
	}
	// Nothing else is measurable on a cold platform.
	if got.Assessment.Confidence != nil {
		t.Errorf("confidence must be withheld with no resolved record, got %v", *got.Assessment.Confidence)
	}
	if got.Assessment.ExpectedDrawdown != nil {
		t.Errorf("expected drawdown must be withheld with no bars, got %v", *got.Assessment.ExpectedDrawdown)
	}
	if got.Assessment.ExpectedReturn != nil {
		t.Error("expected return must be withheld with no expectancy table")
	}
	if len(got.Assessment.Withheld) < 3 {
		t.Errorf("every withheld field must be named with a reason, got %v", got.Assessment.Withheld)
	}
	if len(got.Assessment.Reasons) == 0 {
		t.Error("an empty assessment must still explain itself")
	}
	// The note must disclose that the drawdown measurement is unconditional rather
	// than implying it is state-specific.
	if !strings.Contains(got.Note, "UNCONDITIONAL") {
		t.Errorf("the note must state the drawdown's scope: %q", got.Note)
	}
}

// With enough bars, the adverse excursion becomes measurable and reports the DIP,
// not the close — the number the package exists to provide.
func TestConfidenceMeasuresAdverseExcursionFromBars(t *testing.T) {
	srv, st := newConfidenceServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 60 sessions that each open at 100, dip 4% intraday, and close back at 100.
	// A 1-day hold therefore has a 4% adverse excursion and a ~0% return.
	bars := make([]md.Bar, 0, 60)
	for i := 1; i <= 60; i++ {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: int64(i) * 86400,
			Open: 100, High: 100.5, Low: 96, Close: 100, Volume: 1000,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("bars: %v", err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 60 * 86400,
		RawProb: 0.7, CalProb: 0.7, NUsed: 10, Components: "{}",
	}); err != nil {
		t.Fatalf("prediction: %v", err)
	}

	var got confidenceResp
	if code := getFleetJSON(t, srv.URL+"/api/confidence?symbol=AAA&market=stocks&horizon=1d", &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !got.Excursion.Valid {
		t.Fatalf("60 episodes should clear the floor: %s", got.Excursion.Note)
	}
	if got.Assessment.ExpectedDrawdown == nil {
		t.Fatal("a measurable excursion must populate expected drawdown")
	}
	if d := *got.Assessment.ExpectedDrawdown; d < 0.039 || d > 0.041 {
		t.Errorf("expected drawdown should be the ~4%% intraday dip, got %.4f", d)
	}
	if got.Assessment.DrawdownTail == nil {
		t.Error("the tail should be populated alongside the mean")
	}
	if got.HoldBars != 1 {
		t.Errorf("a 1d horizon holds 1 bar, got %d", got.HoldBars)
	}
}

// A 1w horizon must measure a five-session hold, not a one-session one.
func TestConfidenceHoldLengthFollowsHorizon(t *testing.T) {
	if got := holdBarsFor(md.H1w); got != 5 {
		t.Errorf("1w should hold 5 sessions, got %d", got)
	}
	if got := holdBarsFor(md.H1d); got != 1 {
		t.Errorf("1d should hold 1 session, got %d", got)
	}
}
