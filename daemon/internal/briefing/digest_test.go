package briefing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

func digestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "digest.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// digestClock is a Sunday 18:00 ET instant (past the 17:00 gate).
func digestClock(t *testing.T) (time.Time, *time.Location) {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("loc: %v", err)
	}
	now := time.Date(2026, 7, 19, 18, 0, 0, 0, loc) // a Sunday
	if now.Weekday() != time.Sunday {
		t.Fatal("fixture must be a Sunday")
	}
	return now, loc
}

// seedDigestFixture plants a regime change (frozen "downtrend" early in the
// week, current forecast "uptrend"), 3 extra high-conviction forecasts, and a
// resolved record for one kind.
func seedDigestFixture(t *testing.T, st *store.Store, now time.Time) {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}
	weekStart := now.Add(-6 * 24 * time.Hour).Unix()
	// Frozen call early in the week said "downtrend"…
	if _, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{
		SymbolID: sym.ID, Kind: structregime.KindTrend21, Ts: weekStart, HorizonDays: 21,
		Regime: "downtrend", Conviction: 0.9, HistoricalAccuracy: 0.972, Rank: 0.9,
	}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	// …the current forecast says "uptrend" — a change.
	if err := st.UpsertRegimeForecast(ctx, sym.ID, now.Unix(), structregime.Forecast{
		Kind: structregime.KindTrend21, HorizonDays: 21, Regime: "uptrend",
		Conviction: 0.93, HistoricalAccuracy: 0.972, Tier: "very-high conviction", Rank: 0.93, N: 300,
	}); err != nil {
		t.Fatalf("forecast: %v", err)
	}
	// A resolved record (below the 30 gate → the digest must say so).
	old := now.Add(-40 * 24 * time.Hour).Unix()
	if _, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{
		SymbolID: sym.ID, Kind: structregime.KindLiquidity21, Ts: old, HorizonDays: 21,
		Regime: "active", Conviction: 0.8, HistoricalAccuracy: 0.876, Rank: 0.9,
	}); err != nil {
		t.Fatalf("freeze2: %v", err)
	}
	due, err := st.DueRegimeOutcomes(ctx, 1<<60, 0)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	for _, row := range due {
		if err := st.ResolveRegimeOutcome(ctx, row.ID, row.Regime, true, now.Unix()-3*86400); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
}

// Week gate: the digest fires exactly once per NY week; the second run in the
// same week waits, and before Sunday 17:00 nothing fires.
func TestDigestWeekGateIdempotent(t *testing.T) {
	st := digestStore(t)
	now, loc := digestClock(t)
	w := &DigestWorker{St: st, Loc: loc, Now: func() time.Time { return now }}

	detail, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if !strings.Contains(detail, "composed weekly digest") {
		t.Fatalf("first run should compose, got %q", detail)
	}
	detail2, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if !strings.Contains(detail2, "waiting") {
		t.Fatalf("second run in the same week must wait, got %q", detail2)
	}

	// Before the Sunday-17:00 gate of a fresh week nothing fires.
	early := &DigestWorker{St: digestStore(t), Loc: loc,
		Now: func() time.Time { return now.Add(-3 * time.Hour) }} // Sunday 15:00
	if d3, err := early.Run(context.Background()); err != nil || !strings.Contains(d3, "waiting") {
		t.Fatalf("pre-gate run must wait, got %q err=%v", d3, err)
	}
}

// No transport: the delivery is an honest no-op — the text is still composed
// and stored for /api/digest, sentAt stays unset, and the detail says why.
func TestDigestNoTransportNoOp(t *testing.T) {
	st := digestStore(t)
	now, loc := digestClock(t)
	w := &DigestWorker{St: st, Notifier: nil, Loc: loc, Now: func() time.Time { return now }}

	detail, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "digest skipped: no transport") {
		t.Fatalf("detail must state the no-op, got %q", detail)
	}
	text, err := st.GetMeta(context.Background(), MetaDigestLastText)
	if err != nil || text == "" {
		t.Fatalf("digest text must be stored even without a transport (err=%v)", err)
	}
	sent, _ := st.GetMeta(context.Background(), MetaDigestSentAt)
	if sent != "" {
		t.Fatalf("sentAt must stay unset without a transport, got %q", sent)
	}
}

// Content assembly: the fixture's regime change, gated record line, and top
// calls all appear in the composed text; a configured transport receives it.
func TestDigestContentAssemblyAndDelivery(t *testing.T) {
	st := digestStore(t)
	now, loc := digestClock(t)
	seedDigestFixture(t, st, now)

	var delivered string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		delivered += string(body)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	n := &notify.Notifier{WebhookURL: srv.URL}

	w := &DigestWorker{St: st, Notifier: n, Loc: loc, Now: func() time.Time { return now }}
	detail, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "delivered to webhook") {
		t.Fatalf("configured transport must be reported, got %q", detail)
	}
	text, _ := st.GetMeta(context.Background(), MetaDigestLastText)
	for _, want := range []string{
		"AAA trend21 downtrend→uptrend", // the regime change
		"liquidity21 1 resolved",        // the record line
		"not yet significant — 1/30",    // the honesty gate
		`AAA trend21 "uptrend"`,         // the top call
		"not a forecast",                // the disclaimer survives
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("digest text missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(delivered, "weekly digest") {
		t.Fatalf("webhook did not receive the digest, got %q", delivered)
	}
	sent, _ := st.GetMeta(context.Background(), MetaDigestSentAt)
	if sent == "" {
		t.Fatal("sentAt must be recorded after delivery")
	}
}
