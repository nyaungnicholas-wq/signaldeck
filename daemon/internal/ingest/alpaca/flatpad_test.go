package alpaca

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// barsJSON renders a one-page /bars response.
func barsJSON(rows []string) string {
	out := `{"bars":[`
	for i, r := range rows {
		if i > 0 {
			out += ","
		}
		out += r
	}
	return out + `],"next_page_token":null}`
}

// A vendor pad must never be STORED. Alpaca fabricates a session when the feed
// saw no trade — volume 0, open=high=low=close — and stored it reads downstream
// as a real session returning exactly 0.0%.
func TestBackfillDaily_DropsVendorPadsAndKeepsRealBars(t *testing.T) {
	fastPages(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, barsJSON([]string{
			// real session
			`{"t":"2025-04-02T04:00:00Z","o":1.19,"h":1.31,"l":1.15,"c":1.22,"v":316100}`,
			// the genuine final trading day: violent, and it MUST survive
			`{"t":"2025-04-03T04:00:00Z","o":1.18,"h":1.18,"l":0.45,"c":0.6695,"v":2782368}`,
			// three vendor pads
			`{"t":"2025-04-04T04:00:00Z","o":0.6695,"h":0.6695,"l":0.6695,"c":0.6695,"v":0}`,
			`{"t":"2025-04-07T04:00:00Z","o":0.6695,"h":0.6695,"l":0.6695,"c":0.6695,"v":0}`,
			`{"t":"2025-04-08T04:00:00Z","o":0.6695,"h":0.6695,"l":0.6695,"c":0.6695,"v":0}`,
			// zero volume but a real RANGE — not the pad signature, so keep it
			`{"t":"2025-04-09T04:00:00Z","o":0.70,"h":0.75,"l":0.65,"c":0.68,"v":0}`,
			// flat but it TRADED — a stock pinned all session is real
			`{"t":"2025-04-10T04:00:00Z","o":0.80,"h":0.80,"l":0.80,"c":0.80,"v":54000}`,
		}))
	}))
	defer srv.Close()

	st := openTestStore(t)
	sym, err := st.UpsertSymbol(context.Background(), "VRPX", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	c := &Client{BaseData: srv.URL, HTTP: srv.Client()}

	n, err := c.BackfillDaily(context.Background(), st, sym.ID, "VRPX")
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 4 {
		t.Fatalf("stored %d bar(s), want 4 — the three vendor pads must be dropped and "+
			"the returned count must reflect what was actually stored", n)
	}

	bars, err := st.LastBars(context.Background(), sym.ID, md.TF1d, 50)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(bars) != 4 {
		t.Fatalf("read back %d bar(s), want 4", len(bars))
	}
	for _, b := range bars {
		if b.Volume == 0 && b.Open == b.High && b.High == b.Low && b.Low == b.Close {
			t.Fatalf("a vendor pad was stored: ts=%d px=%v", b.Ts, b.Close)
		}
	}
	// The delisting-day bar is the most informative one a dead name has; losing it
	// to an over-broad filter would be worse than the padding.
	var kept bool
	for _, b := range bars {
		if b.Close == 0.6695 && b.Volume == 2782368 {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the genuine final trading day was dropped — the filter is too broad")
	}
}

// The restriction to DAILY is load-bearing: a minute with no trades is ordinary,
// and crypto alone holds tens of thousands of legitimate flat zero-volume 1m bars.
func TestBackfillMinute_KeepsFlatZeroVolumeBars(t *testing.T) {
	fastPages(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, barsJSON([]string{
			`{"t":"2025-04-02T14:30:00Z","o":1.20,"h":1.21,"l":1.19,"c":1.20,"v":1000}`,
			`{"t":"2025-04-02T14:31:00Z","o":1.20,"h":1.20,"l":1.20,"c":1.20,"v":0}`,
			`{"t":"2025-04-02T14:32:00Z","o":1.20,"h":1.20,"l":1.20,"c":1.20,"v":0}`,
		}))
	}))
	defer srv.Close()

	st := openTestStore(t)
	sym, _ := st.UpsertSymbol(context.Background(), "AAA", md.Stocks, "")
	c := &Client{BaseData: srv.URL, HTTP: srv.Client()}

	n, err := c.BackfillMinute(context.Background(), st, sym.ID, "AAA")
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 3 {
		t.Fatalf("stored %d minute bar(s), want all 3 — a minute with no trades is "+
			"ordinary and must not be filtered", n)
	}
}

// The predicate itself, stated as a table so the boundary is explicit.
func TestSyntheticDailyPad(t *testing.T) {
	flat := md.Bar{Open: 5, High: 5, Low: 5, Close: 5, Volume: 0}
	cases := []struct {
		name string
		tf   md.Timeframe
		bar  md.Bar
		want bool
	}{
		{"daily flat zero-volume is a pad", md.TF1d, flat, true},
		{"minute flat zero-volume is a real quiet minute", md.TF1m, flat, false},
		{"hourly flat zero-volume is left alone", md.TF1h, flat, false},
		{"daily flat WITH volume is a pinned session", md.TF1d,
			md.Bar{Open: 5, High: 5, Low: 5, Close: 5, Volume: 100}, false},
		{"daily zero-volume WITH range is not the signature", md.TF1d,
			md.Bar{Open: 5, High: 6, Low: 4, Close: 5, Volume: 0}, false},
	}
	for _, tc := range cases {
		if got := syntheticDailyPad(tc.tf, tc.bar); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
