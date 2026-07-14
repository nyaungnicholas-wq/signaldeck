package cboe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// dailyFixture mirrors the live document shape (probed + verified
// 2026-07-10): ratio values as strings, volume sections as numbers.
const dailyFixture = `{
  "ratios": [
    {"name": "TOTAL PUT/CALL RATIO", "value": "0.86"},
    {"name": "INDEX PUT/CALL RATIO", "value": "1.01"},
    {"name": "EQUITY PUT/CALL RATIO", "value": "0.57"},
    {"name": "CBOE VOLATILITY INDEX (VIX) PUT/CALL RATIO", "value": "0.51"},
    {"name": "OEX PUT/CALL RATIO", "value": "0.00"}
  ],
  "SUM OF ALL PRODUCTS": {"name": "VOLUME", "call": 6791245, "put": 5841232, "total": 12632477},
  "EQUITY OPTIONS": {"name": "VOLUME", "call": 2408403, "put": 1371710, "total": 3780113}
}`

func TestParseDaily(t *testing.T) {
	s, err := ParseDaily([]byte(dailyFixture), "2026-07-09")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Day != "2026-07-09" || s.TotalPC != 0.86 || s.IndexPC != 1.01 ||
		s.EquityPC != 0.57 || s.VIXPC != 0.51 {
		t.Errorf("ratios parsed wrong: %+v", s)
	}
	if s.CallVol != 6791245 || s.PutVol != 5841232 || s.TotalVol != 12632477 {
		t.Errorf("volumes parsed wrong: %+v", s)
	}
}

func TestParseDailyMissingHeadlineRatio(t *testing.T) {
	if _, err := ParseDaily([]byte(`{"ratios":[{"name":"SOMETHING ELSE","value":"1"}]}`), "2026-07-09"); err == nil {
		t.Error("a document without TOTAL PUT/CALL RATIO must fail loudly")
	}
}

func TestFetchDaily(t *testing.T) {
	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA = r.URL.Path, r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/2026-07-09_daily_options":
			_, _ = w.Write([]byte(dailyFixture))
		case "/2026-07-05_daily_options":
			// CBOE's CDN 403s absent days (Sunday here).
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, UA: "test-ua", MinInterval: time.Millisecond}

	s, err := c.FetchDaily(context.Background(), time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC))
	if err != nil || s.TotalPC != 0.86 {
		t.Fatalf("fetch: %+v err=%v", s, err)
	}
	if gotPath != "/2026-07-09_daily_options" || gotUA != "test-ua" {
		t.Errorf("request wrong: path=%q ua=%q", gotPath, gotUA)
	}

	if _, err := c.FetchDaily(context.Background(), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrNotAvailable) {
		t.Errorf("403 must map to ErrNotAvailable, got %v", err)
	}
	if _, err := c.FetchDaily(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err == nil || errors.Is(err, ErrNotAvailable) {
		t.Errorf("502 must be a real error, got %v", err)
	}
}
