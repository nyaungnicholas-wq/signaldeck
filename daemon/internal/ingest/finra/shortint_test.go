package finra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const siFixture = `accountingYearMonthNumber|symbolCode|issueName|issuerServicesGroupExchangeCode|marketClassCode|currentShortPositionQuantity|previousShortPositionQuantity|stockSplitFlag|averageDailyVolumeQuantity|daysToCoverQuantity|revisionFlag|changePercent|changePreviousNumber|settlementDate
20260615|A|Agilent Technologies Inc.|A|NYSE|5662129|5218592||2257805|2.51||8.50|443537|2026-06-15
20260615|AA|Alcoa Corporation|A|NYSE|8091919|6518514||5508179|1.47||24.14|1573405|2026-06-15
20260615|NEWCO|Fresh Listing Corp|A|NYSE|1000||||||||2026-06-15
garbage-single-line
20260615|BAD|Bad Row|A|NYSE|notanumber|1|||||1|1|2026-06-15
3
`

func TestParseShortInterest(t *testing.T) {
	rows, skipped, err := ParseShortInterest(strings.NewReader(siFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// garbage-single-line (non-integer) + BAD row = 2 skipped; trailer "3" tolerated.
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	a := rows[0]
	if a.Symbol != "A" || a.Settlement != "2026-06-15" || a.ShortQty != 5662129 ||
		a.PrevQty != 5218592 || a.ADV != 2257805 || a.DaysToCover != 2.51 || a.ChangePct != 8.50 {
		t.Errorf("row A parsed wrong: %+v", a)
	}
	// Blank optional cells (new listing) parse leniently to 0, never dropped.
	nc := rows[2]
	if nc.Symbol != "NEWCO" || nc.ShortQty != 1000 || nc.PrevQty != 0 || nc.DaysToCover != 0 {
		t.Errorf("NEWCO leniency wrong: %+v", nc)
	}
}

func TestParseShortInterestHeaderMismatchFailsLoudly(t *testing.T) {
	if _, _, err := ParseShortInterest(strings.NewReader("some|other|header\n1|2|3\n")); err == nil {
		t.Fatal("format change must fail loudly, not parse garbage")
	}
}

func TestSettlementDates(t *testing.T) {
	// 2026-07-10: most recent settlements are Jun 30, Jun 15, May 31, May 15…
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	got := SettlementDates(now, 4)
	want := []string{"2026-06-30", "2026-06-15", "2026-05-31", "2026-05-15"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Format("2006-01-02") != w {
			t.Errorf("dates[%d] = %s, want %s", i, got[i].Format("2006-01-02"), w)
		}
	}
	// On the 16th, the 15th is the newest; year boundary walks into December.
	got2 := SettlementDates(time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC), 3)
	want2 := []string{"2026-01-15", "2025-12-31", "2025-12-15"}
	for i, w := range want2 {
		if got2[i].Format("2006-01-02") != w {
			t.Errorf("jan dates[%d] = %s, want %s", i, got2[i].Format("2006-01-02"), w)
		}
	}
}

func TestSIClientFetch(t *testing.T) {
	var gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotPath = r.Header.Get("User-Agent"), r.URL.Path
		switch r.URL.Path {
		case "/shrt20260615.csv":
			_, _ = w.Write([]byte(siFixture))
		case "/shrt20260630.csv":
			// FINRA's CDN 403s not-yet-published settlement dates.
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	c := &SIClient{BaseURL: srv.URL, UA: "test-ua", MinInterval: time.Millisecond}

	rows, _, err := c.FetchShortInterest(context.Background(), time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC))
	if err != nil || len(rows) != 3 {
		t.Fatalf("fetch ok day: rows=%d err=%v", len(rows), err)
	}
	if gotUA != "test-ua" || gotPath != "/shrt20260615.csv" {
		t.Errorf("request wrong: ua=%q path=%q", gotUA, gotPath)
	}

	_, _, err = c.FetchShortInterest(context.Background(), time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrNotAvailable) {
		t.Errorf("403 must map to ErrNotAvailable, got %v", err)
	}

	_, _, err = c.FetchShortInterest(context.Background(), time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC))
	if err == nil || errors.Is(err, ErrNotAvailable) {
		t.Errorf("500 must be a real error, got %v", err)
	}
}
