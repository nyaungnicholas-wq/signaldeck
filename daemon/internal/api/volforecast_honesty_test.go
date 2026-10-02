package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// RV-COPY (2026-10-02): the record's caveat and its pooled figures must say
// no more, and count no more, than the registered grade does.

type rvBodyHorizon struct {
	Horizon      int    `json:"horizon"`
	N            int    `json:"n"`
	DistinctDays int    `json:"distinctDays"`
	Verdict      string `json:"verdict"`
}

type rvBody struct {
	Caveat   string          `json:"caveat"`
	Horizons []rvBodyHorizon `json:"horizons"`
}

func readRVBody(t *testing.T, d Deps) rvBody {
	t.Helper()
	rr := httptest.NewRecorder()
	d.volForecastRecord(rr, httptest.NewRequest("GET", "/api/vol-forecast/record", nil))
	if rr.Code != 200 {
		t.Fatalf("unexpected status %d: %s", rr.Code, rr.Body.String())
	}
	var rb rvBody
	if err := json.Unmarshal(rr.Body.Bytes(), &rb); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	return rb
}

func TestVolRecordCaveatMatchesTheVerdict(t *testing.T) {
	reg := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()

	// Part A (pass)
	_, st, d := newTestServer(t, nil)
	seedRVStudy(t, st, reg, reg+60, reg+86400, 64)
	b := readRVBody(t, d)
	var h1 rvBodyHorizon
	found := false
	for _, h := range b.Horizons {
		if h.Horizon == 1 {
			h1 = h
			found = true
			break
		}
	}
	if !found {
		t.Fatal("horizon 1 not found")
	}
	if h1.Verdict != "BEATS THE NULLS" {
		t.Fatalf("horizon 1 verdict %q, want BEATS THE NULLS", h1.Verdict)
	}
	if strings.Contains(b.Caveat, "not a claim of skill") {
		t.Errorf("caveat should not contain 'not a claim of skill', got %q", b.Caveat)
	}
	if !strings.Contains(b.Caveat, "not a guarantee of future accuracy") {
		t.Errorf("caveat should contain 'not a guarantee of future accuracy', got %q", b.Caveat)
	}
	if !strings.Contains(b.Caveat, "Over 64 distinct trading days") {
		t.Errorf("caveat should contain 'Over 64 distinct trading days', got %q", b.Caveat)
	}
	if b.Caveat == RVRecordCaveat {
		t.Errorf("caveat should not be the generic accruing caveat, got %q", b.Caveat)
	}

	// Part B (not a pass)
	_, st2, d2 := newTestServer(t, nil)
	seedRVStudy(t, st2, reg, reg+60, reg+86400, 20)
	b2 := readRVBody(t, d2)
	var h1b2 rvBodyHorizon
	found2 := false
	for _, h := range b2.Horizons {
		if h.Horizon == 1 {
			h1b2 = h
			found2 = true
			break
		}
	}
	if !found2 {
		t.Fatal("horizon 1 not found in second test")
	}
	if h1b2.Verdict != "INSUFFICIENT" {
		t.Fatalf("horizon 1 verdict %q, want INSUFFICIENT", h1b2.Verdict)
	}
	if b2.Caveat != RVRecordCaveat {
		t.Errorf("caveat mismatch: got %q, want %q", b2.Caveat, RVRecordCaveat)
	}
}

func TestVolRecordPooledFiguresObeyTheStartRule(t *testing.T) {
	// The pooled figures (n and the meanQlike / vsEwma means) obey the
	// registered start rule exactly as the grade does: forecasts frozen AT
	// the registration second are outside the live window.
	reg := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()
	_, st, d := newTestServer(t, nil)
	// frozen AT the registration second: excluded
	seedRVStudy(t, st, reg, reg, reg-90*86400, 50)
	// the live window (regTs 0 appends no second registration)
	seedRVStudy(t, st, 0, reg+60, reg+86400, 64)
	b := readRVBody(t, d)
	var h1, h5 rvBodyHorizon
	found1, found5 := false, false
	for _, h := range b.Horizons {
		switch h.Horizon {
		case 1:
			h1 = h
			found1 = true
		case 5:
			h5 = h
			found5 = true
		}
	}
	if !found1 {
		t.Fatal("horizon 1 not found")
	}
	if !found5 {
		t.Fatal("horizon 5 not found")
	}
	expectedN := 64 * 30 // live rows only: 30 symbols x 64 days
	if h1.N != expectedN {
		t.Errorf("horizon 1: N = %d, want %d", h1.N, expectedN)
	}
	if h5.N != expectedN {
		t.Errorf("horizon 5: N = %d, want %d", h5.N, expectedN)
	}
	if h1.DistinctDays != 64 {
		t.Errorf("horizon 1: DistinctDays = %d, want 64", h1.DistinctDays)
	}
	if h1.Verdict != "BEATS THE NULLS" {
		t.Errorf("horizon 1: Verdict = %q, want BEATS THE NULLS", h1.Verdict)
	}
}
