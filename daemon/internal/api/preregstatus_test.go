package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// E-CAVEAT-DATE (2026-10-02): /api/prereg said every advertised accuracy is a
// BACKTEST result "until the live record starts arriving on 2026-08-07", which
// read as the current state long after resolutions began. It now states the
// live status from the same count every regime caveat uses: none yet, or how
// many resolved, never how they scored.
func TestPreregLiveStatusSentence(t *testing.T) {
	tests := []struct {
		in   any
		want string
		nil  bool
	}{
		{0, "No call has resolved live yet", false},
		{1, "1 call has resolved live so far", false},
		{3, "3 calls have resolved live so far", false},
		{nil, "separate measurement", true},
	}
	forbidden := []string{"%", "accura", "skill", "until the live record"}
	for _, tt := range tests {
		got := preregLiveStatus(tt.in)
		if !strings.Contains(got, tt.want) {
			t.Fatalf("preregLiveStatus(%v) = %q, want substring %q", tt.in, got, tt.want)
		}
		for _, f := range forbidden {
			if strings.Contains(got, f) {
				t.Fatalf("preregLiveStatus(%v) = %q, contains forbidden %q", tt.in, got, f)
			}
		}
		if tt.nil && strings.Contains(got, "No call") {
			t.Fatalf("preregLiveStatus(nil) = %q, should not contain 'No call'", got)
		}
	}
}

func TestPreregServesTheCurrentLiveStatus(t *testing.T) {
	ctx := context.Background()
	_, st, d := newTestServer(t, nil)

	get := func() (*int, string) {
		req := httptest.NewRequest("GET", "/api/prereg", nil)
		rr := httptest.NewRecorder()
		d.prereg(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
		}
		var resp struct {
			LiveResolved *int   `json:"liveResolved"`
			WhatThisIs   string `json:"whatThisIs"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.LiveResolved == nil {
			t.Fatalf("LiveResolved is nil in response")
		}
		return resp.LiveResolved, resp.WhatThisIs
	}

	liveResolved, whatThisIs := get()
	if *liveResolved != 0 {
		t.Fatalf("expected liveResolved=0, got %d", *liveResolved)
	}
	if !strings.Contains(whatThisIs, "No call has resolved live yet") {
		t.Fatalf(`whatThisIs = %q, want substring "No call has resolved live yet"`, whatThisIs)
	}
	if strings.Contains(whatThisIs, "until the live record starts arriving") {
		t.Fatalf(`whatThisIs = %q, should not contain "until the live record starts arriving"`, whatThisIs)
	}

	sym, err := st.UpsertSymbol(ctx, "PRA", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	base := store.GradingEpoch + 86400
	ts1 := base
	ts2 := base + 3*86400

	_, err = st.InsertRegimeOutcome(ctx, store.RegimeCall{
		SymbolID:           sym.ID,
		Kind:               structregime.KindTrend21,
		Ts:                 ts1,
		HorizonDays:        21,
		Regime:             "uptrend",
		Conviction:         0.9,
		HistoricalAccuracy: 0.97,
		Rank:               0.9,
		NaiveLabel:         "uptrend",
	})
	if err != nil {
		t.Fatalf("InsertRegimeOutcome (first): %v", err)
	}

	_, err = st.InsertRegimeOutcome(ctx, store.RegimeCall{
		SymbolID:           sym.ID,
		Kind:               structregime.KindTrend21,
		Ts:                 ts2,
		HorizonDays:        21,
		Regime:             "uptrend",
		Conviction:         0.9,
		HistoricalAccuracy: 0.97,
		Rank:               0.9,
		NaiveLabel:         "uptrend",
	})
	if err != nil {
		t.Fatalf("InsertRegimeOutcome (second): %v", err)
	}

	now := base + 400*86400
	rows, err := st.DueRegimeOutcomes(ctx, now, 100)
	if err != nil {
		t.Fatalf("DueRegimeOutcomes: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 due rows, got %d", len(rows))
	}

	resolvedAt := base + 40*86400
	for _, row := range rows {
		err = st.ResolveRegimeOutcome(ctx, row.ID, "uptrend", true, resolvedAt)
		if err != nil {
			t.Fatalf("ResolveRegimeOutcome: %v", err)
		}
	}

	liveResolved, whatThisIs = get()
	if *liveResolved != 2 {
		t.Fatalf("expected liveResolved=2, got %d", *liveResolved)
	}
	if !strings.Contains(whatThisIs, "2 calls have resolved live so far") {
		t.Fatalf(`whatThisIs = %q, want substring "2 calls have resolved live so far"`, whatThisIs)
	}
	if strings.Contains(whatThisIs, "No call has resolved") {
		t.Fatalf(`whatThisIs = %q, should not contain "No call has resolved"`, whatThisIs)
	}
}
