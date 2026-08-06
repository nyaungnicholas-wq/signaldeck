package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A row with NUsed==0 is EVIDENCE, not a forecast: no leg was admitted, so the
// blend's 0.5 means "nothing was measured", not "a coin flip is likely". These
// tests pin the two halves of that contract, because getting either wrong
// reintroduces a defect this repo has already shipped once — 1,202 live rows
// with raw_prob=0.5 and nUsed=0 published and then graded as real forecasts.
func TestEvidenceRowIsRetainedButNeverEntersTheGradedPopulation(t *testing.T) {
	st := openDashStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "EVID", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	const comps = `{"ExpectancyHitRate":0.42,"PressureScore":-0.3}`
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1000,
		RawProb: 0.5, CalProb: 0.5, NUsed: 0, Components: comps,
	}); err != nil {
		t.Fatalf("UpsertPrediction: %v", err)
	}

	// KEPT: the components survive. This is the whole reason the row is written
	// at all — it is the only surface recording what each leg said, and the leg
	// graders read nothing else.
	var got string
	if err := st.db.QueryRowContext(ctx,
		`SELECT components FROM predictions WHERE symbol_id=? AND horizon=? AND ts=?`,
		sym.ID, string(md.H1d), 1000).Scan(&got); err != nil {
		t.Fatalf("evidence row was not retained: %v", err)
	}
	if got != comps {
		t.Fatalf("components = %q, want %q", got, comps)
	}

	// NOT GRADED: no outcome row exists, so no grader, calibration fit or live
	// record can ever see it.
	var outcomes int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=? AND horizon=? AND ts=?`,
		sym.ID, string(md.H1d), 1000).Scan(&outcomes); err != nil {
		t.Fatalf("count outcomes: %v", err)
	}
	if outcomes != 0 {
		t.Fatalf("evidence row seeded %d outcome rows; want 0 — a legless 0.5 must never be gradable", outcomes)
	}

	// NOT SERVED: the newest row for this symbol is the evidence row, and the
	// published surface must report "no forecast" rather than fall through to
	// it. Getting this wrong is how a 0.5 reaches a user as a real call.
	if _, ok, err := st.LatestPrediction(ctx, sym.ID, md.H1d); err != nil || ok {
		t.Fatalf("LatestPrediction returned ok=%v (err=%v); an evidence row must not be served", ok, err)
	}
}

// A real forecast written LATER must still be served, and an evidence row that
// arrives after it must not hide it — the filter has to live inside the
// latest-row selection, not just around it, or a symbol disappears from the
// product the moment its legs are benched.
func TestEvidenceRowDoesNotHideTheLastRealForecast(t *testing.T) {
	st := openDashStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "EVID2", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1000,
		RawProb: 0.71, CalProb: 0.71, NUsed: 2, Components: `{}`,
	}); err != nil {
		t.Fatalf("seed forecast: %v", err)
	}
	// Newer, but legless.
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 2000,
		RawProb: 0.5, CalProb: 0.5, NUsed: 0, Components: `{}`,
	}); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}

	p, ok, err := st.LatestPrediction(ctx, sym.ID, md.H1d)
	if err != nil || !ok {
		t.Fatalf("LatestPrediction: ok=%v err=%v; want the last REAL forecast", ok, err)
	}
	if p.Ts != 1000 || p.CalProb != 0.71 {
		t.Fatalf("served ts=%d prob=%v; want the ts=1000 forecast at 0.71, not the newer evidence row", p.Ts, p.CalProb)
	}
}

// EvidenceDayBySymbol is what keeps a legless blend to ONE row a day instead of
// one per pass. It must report the trading day (not the UTC day) and must see
// only evidence rows, or the runner would either spam the table or stop
// recording evidence altogether.
func TestEvidenceDayBySymbolSeesOnlyEvidenceRows(t *testing.T) {
	st := openDashStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "EVID3", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	// A real forecast must NOT register as evidence.
	tsForecast := int64(1785816000) // a stock daily bar stamp (ET midnight)
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: tsForecast,
		RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: `{}`,
	}); err != nil {
		t.Fatalf("seed forecast: %v", err)
	}
	got, err := st.EvidenceDayBySymbol(ctx, md.H1d)
	if err != nil {
		t.Fatalf("EvidenceDayBySymbol: %v", err)
	}
	if _, ok := got[sym.ID]; ok {
		t.Fatalf("a real forecast was counted as evidence: %v", got)
	}

	tsEvidence := tsForecast + 3600
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: tsEvidence,
		RawProb: 0.5, CalProb: 0.5, NUsed: 0, Components: `{}`,
	}); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	got, err = st.EvidenceDayBySymbol(ctx, md.H1d)
	if err != nil {
		t.Fatalf("EvidenceDayBySymbol: %v", err)
	}
	day, ok := got[sym.ID]
	if !ok {
		t.Fatalf("evidence row not reported: %v", got)
	}
	if want := md.TradingDay(tsEvidence); day != want {
		t.Fatalf("day = %d, want the TRADING day %d (a UTC-day fold would drift)", day, want)
	}
}
