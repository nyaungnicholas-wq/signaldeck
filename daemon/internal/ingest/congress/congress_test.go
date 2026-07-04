package congress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func TestParseSenateFixture(t *testing.T) {
	trades, err := ParseSenate(fixture(t, "senate_transactions.json"))
	if err != nil {
		t.Fatalf("ParseSenate: %v", err)
	}
	// 4 rows in the fixture, but the "--" ticker (municipal bond) is dropped.
	if len(trades) != 3 {
		t.Fatalf("trades = %d, want 3 (bond row dropped)", len(trades))
	}

	aapl := trades[0]
	if aapl.Chamber != ChamberSenate || aapl.Member != "Jane Q Senator" ||
		aapl.Ticker != "AAPL" || aapl.TxType != "purchase" ||
		aapl.Amount != "$15,001 - $50,000" {
		t.Errorf("aapl row = %+v", aapl)
	}
	// 05/12/2026 UTC midnight.
	wantTx := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC).Unix()
	wantDisc := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC).Unix()
	if aapl.TxTs != wantTx || aapl.DisclosedTs != wantDisc {
		t.Errorf("aapl dates = %d/%d, want %d/%d", aapl.TxTs, aapl.DisclosedTs, wantTx, wantDisc)
	}
	// The legal lag is visible in the data itself (disclosed ~39d after trade).
	if lag := aapl.DisclosedTs - aapl.TxTs; lag < 30*86400 || lag > 45*86400 {
		t.Errorf("fixture lag = %dd (fixture should demonstrate the 30-45d window)", lag/86400)
	}

	if trades[1].TxType != "sale_full" || trades[1].Ticker != "ZZTOP" {
		t.Errorf("sale row = %+v", trades[1])
	}
	// HTML-wrapped ticker sanitized to BA.
	if trades[2].Ticker != "BA" || trades[2].TxType != "sale_partial" {
		t.Errorf("html-ticker row = %+v", trades[2])
	}
	for _, tr := range trades {
		if tr.ID == "" || len(tr.ID) != 40 {
			t.Errorf("bad ID %q on %+v", tr.ID, tr)
		}
	}
}

func TestParseHouseFixture(t *testing.T) {
	trades, err := ParseHouse(fixture(t, "house_transactions.json"))
	if err != nil {
		t.Fatalf("ParseHouse: %v", err)
	}
	// 3 rows; the N/A-ticker treasury note is dropped.
	if len(trades) != 2 {
		t.Fatalf("trades = %d, want 2 (N/A row dropped)", len(trades))
	}
	nvda := trades[0]
	if nvda.Chamber != ChamberHouse || nvda.Member != "Hon. Alexis Example" ||
		nvda.Ticker != "NVDA" || nvda.TxType != "purchase" {
		t.Errorf("nvda row = %+v", nvda)
	}
	if want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix(); nvda.TxTs != want {
		t.Errorf("nvda TxTs = %d, want %d", nvda.TxTs, want)
	}
	// Unparseable transaction_date ("--") stays 0 — honest absence, row kept.
	tsla := trades[1]
	if tsla.Ticker != "TSLA" || tsla.TxTs != 0 || tsla.DisclosedTs == 0 {
		t.Errorf("tsla row = %+v", tsla)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	if _, err := ParseSenate([]byte(`{"not":"an array"}`)); err == nil {
		t.Error("senate: want error on non-array JSON")
	}
	if _, err := ParseHouse([]byte(`garbage`)); err == nil {
		t.Error("house: want error on garbage")
	}
}

func TestTradeIDDeterministicAndDistinct(t *testing.T) {
	a := TradeID("senate", "Jane Q Senator", "AAPL", "purchase", "$1,001 - $15,000", "05/12/2026", "06/20/2026", "Self")
	b := TradeID("senate", "Jane Q Senator", "AAPL", "purchase", "$1,001 - $15,000", "05/12/2026", "06/20/2026", "Self")
	if a != b {
		t.Errorf("same fields must hash equal: %s vs %s", a, b)
	}
	// Case/whitespace-insensitive (mirror re-exports vary).
	c := TradeID("SENATE", " jane q senator ", "aapl", "PURCHASE", "$1,001 - $15,000", "05/12/2026", "06/20/2026", "self")
	if a != c {
		t.Errorf("normalization must make %s == %s", a, c)
	}
	// Any differing field must change the ID.
	d := TradeID("senate", "Jane Q Senator", "AAPL", "purchase", "$1,001 - $15,000", "05/13/2026", "06/20/2026", "Self")
	if a == d {
		t.Error("different tx date must yield a different ID")
	}
	// Field-boundary safety: ("ab","c") vs ("a","bc").
	if TradeID("ab", "c") == TradeID("a", "bc") {
		t.Error("field separator missing — boundary collision")
	}
}

func TestSanitizeTicker(t *testing.T) {
	cases := map[string]string{
		"AAPL":                       "AAPL",
		" nvda ":                     "NVDA",
		"BRK.B":                      "BRK.B",
		"--":                         "",
		"N/A":                        "",
		"":                           "",
		"<a href=\"x\">BA</a>":       "BA",
		"THIS IS NOT A TICKER FIELD": "",
	}
	for in, want := range cases {
		if got := SanitizeTicker(in); got != want {
			t.Errorf("SanitizeTicker(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeTxType(t *testing.T) {
	cases := map[string]string{
		"Purchase":       "purchase",
		"purchase":       "purchase",
		"Sale (Full)":    "sale_full",
		"sale_full":      "sale_full",
		"Sale (Partial)": "sale_partial",
		"sale_partial":   "sale_partial",
		"Sale":           "sale",
		"Exchange":       "exchange",
		"exchange":       "exchange",
		"Received":       "received", // unknown kinds pass through lowercased
	}
	for in, want := range cases {
		if got := NormalizeTxType(in); got != want {
			t.Errorf("NormalizeTxType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchLiveMirrorShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/senate"):
			_, _ = w.Write(fixture(t, "senate_transactions.json"))
		case strings.HasPrefix(r.URL.Path, "/house"):
			_, _ = w.Write(fixture(t, "house_transactions.json"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := New()
	c.SenateURL = srv.URL + "/senate.json"
	c.HouseURL = srv.URL + "/house.json"
	c.MinInterval = time.Millisecond

	sen, err := c.FetchSenate(context.Background())
	if err != nil || len(sen) != 3 {
		t.Fatalf("FetchSenate = %d trades, err %v", len(sen), err)
	}
	hou, err := c.FetchHouse(context.Background())
	if err != nil || len(hou) != 2 {
		t.Fatalf("FetchHouse = %d trades, err %v", len(hou), err)
	}
}

func TestFetchDeadMirror(t *testing.T) {
	// AccessDenied-style 403 (the real mirrors' current behavior, verified
	// 2026-07-04) must surface as an error WITHOUT retry loops on 403.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
	}))
	defer srv.Close()
	c := New()
	c.SenateURL = srv.URL + "/senate.json"
	c.HouseURL = srv.URL + "/house.json"
	c.MinInterval = time.Millisecond

	if _, err := c.FetchSenate(context.Background()); err == nil {
		t.Fatal("want error from 403 mirror")
	} else if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should carry the status: %v", err)
	}

	// A DNS-dead host (connection error) also errors cleanly.
	srv.Close()
	if _, err := c.FetchHouse(context.Background()); err == nil {
		t.Fatal("want error from closed server")
	}
}
