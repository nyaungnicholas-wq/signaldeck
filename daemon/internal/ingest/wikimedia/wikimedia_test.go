package wikimedia

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// pvFixture mirrors the live per-article response (verified 2026-07-10).
const pvFixture = `{"items":[
  {"project":"en.wikipedia","article":"Nvidia","timestamp":"2026070100","views":4826},
  {"project":"en.wikipedia","article":"Nvidia","timestamp":"2026070200","views":5120}
]}`

func TestFetchDaily(t *testing.T) {
	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA = r.URL.Path, r.Header.Get("User-Agent")
		if r.URL.Path == "/Nvidia/daily/2026070100/2026070200" {
			_, _ = w.Write([]byte(pvFixture))
			return
		}
		http.Error(w, `{"type":"not_found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, UA: "test-ua", MinInterval: time.Millisecond}

	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	views, err := c.FetchDaily(context.Background(), "Nvidia", from, to)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotUA != "test-ua" || gotPath != "/Nvidia/daily/2026070100/2026070200" {
		t.Errorf("request wrong: path=%q ua=%q", gotPath, gotUA)
	}
	want := []DayViews{{Day: "2026-07-01", Views: 4826}, {Day: "2026-07-02", Views: 5120}}
	if !reflect.DeepEqual(views, want) {
		t.Errorf("views = %+v, want %+v", views, want)
	}

	if _, err := c.FetchDaily(context.Background(), "No_Such_Page", from, to); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 must map to ErrNotFound, got %v", err)
	}
}

func TestStripCorpSuffixes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Agilent Technologies Inc.", "Agilent Technologies"},
		{"NVIDIA CORP", "NVIDIA"},
		{"Apple Inc.", "Apple"},
		{"Alcoa Corporation", "Alcoa"},
		{"Shopify Inc", "Shopify"},
		{"Diageo plc", "Diageo"},
		{"Barrick Gold Corporation", "Barrick Gold"},
		{"Coca-Cola Co", "Coca-Cola"},
		{"Salesforce, Inc.", "Salesforce"},
		{"Goldman Sachs Group Inc", "Goldman Sachs"},
		{"Ford Motor Co", "Ford Motor"},
		{"Inc", "Inc"}, // never strip down to nothing
	}
	for _, c := range cases {
		if got := StripCorpSuffixes(c.in); got != c.want {
			t.Errorf("StripCorpSuffixes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestArticleCandidates(t *testing.T) {
	got := ArticleCandidates("Agilent Technologies Inc.")
	want := []string{"Agilent_Technologies", "Agilent_Technologies_(company)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}
	if got := ArticleCandidates("   "); got != nil {
		t.Errorf("blank name must yield no candidates, got %v", got)
	}
}
