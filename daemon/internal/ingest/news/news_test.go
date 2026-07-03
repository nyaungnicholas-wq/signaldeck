package news

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// twoArticleFixture is a realistic 2-article /news response with NUMERIC ids and
// RFC3339 created_at times.
const twoArticleFixture = `{
  "news": [
    {
      "id": 24843201,
      "headline": "Apple Unveils New Chip",
      "source": "benzinga",
      "url": "https://example.com/aapl-chip",
      "created_at": "2026-07-01T14:30:00Z",
      "symbols": ["AAPL"]
    },
    {
      "id": 24843198,
      "headline": "Analyst Raises Apple Price Target",
      "source": "reuters",
      "url": "https://example.com/aapl-target",
      "created_at": "2026-07-01T13:05:30Z",
      "symbols": ["AAPL", "SPY"]
    }
  ],
  "next_page_token": null
}`

// newsServer returns an httptest server serving body for /news and recording the
// last request it saw (for header/query assertions).
func newsServer(t *testing.T, status int, body string) (*httptest.Server, *http.Request) {
	t.Helper()
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := *r
		seen = &captured
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() }) //nolint:errcheck
	return st
}

func TestFetch(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		status  int
		wantErr bool
		want    []store.NewsItem
	}{
		{
			name:   "two articles parse",
			body:   twoArticleFixture,
			status: http.StatusOK,
			want: []store.NewsItem{
				{
					ID:       "24843201",
					Ts:       1782916200, // 2026-07-01T14:30:00Z
					Headline: "Apple Unveils New Chip",
					URL:      "https://example.com/aapl-chip",
					Source:   "benzinga",
				},
				{
					ID:       "24843198",
					Ts:       1782911130, // 2026-07-01T13:05:30Z
					Headline: "Analyst Raises Apple Price Target",
					URL:      "https://example.com/aapl-target",
					Source:   "reuters",
				},
			},
		},
		{
			name:   "empty page",
			body:   `{"news": [], "next_page_token": null}`,
			status: http.StatusOK,
			want:   []store.NewsItem{},
		},
		{
			name:    "non-200 status",
			body:    `{"message":"forbidden"}`,
			status:  http.StatusForbidden,
			wantErr: true,
		},
		{
			name:    "malformed created_at",
			body:    `{"news":[{"id":1,"headline":"x","source":"s","url":"u","created_at":"not-a-time","symbols":["AAPL"]}]}`,
			status:  http.StatusOK,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newsServer(t, tt.status, tt.body)
			c := New("k", "s")
			c.Base = srv.URL + "/v1beta1"
			c.HTTP = srv.Client()

			got, err := c.Fetch(context.Background(), "AAPL")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (%d items)", len(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d items, want %d", len(got), len(tt.want))
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("item %d = %+v, want %+v", i, got[i], w)
				}
				// SymbolID/Sentiment must be left zero for the caller.
				if got[i].SymbolID != 0 {
					t.Errorf("item %d SymbolID = %d, want 0 (caller sets it)", i, got[i].SymbolID)
				}
				if got[i].Sentiment != "" {
					t.Errorf("item %d Sentiment = %q, want empty (worker sets it)", i, got[i].Sentiment)
				}
			}
		})
	}
}

// TestFetchRequest asserts the outgoing request carries the credential headers
// and the expected query params.
func TestFetchRequest(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := *r
		seen = &captured
		_, _ = w.Write([]byte(`{"news":[],"next_page_token":null}`))
	}))
	t.Cleanup(srv.Close)

	c := New("mykey", "mysecret")
	c.Base = srv.URL + "/v1beta1"
	c.HTTP = srv.Client()

	if _, err := c.Fetch(context.Background(), "AAPL"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if seen == nil {
		t.Fatal("no request captured")
	}
	if seen.URL.Path != "/v1beta1/news" {
		t.Errorf("path = %q, want /v1beta1/news", seen.URL.Path)
	}
	if got := seen.URL.Query().Get("symbols"); got != "AAPL" {
		t.Errorf("symbols = %q, want AAPL", got)
	}
	if got := seen.URL.Query().Get("limit"); got != "20" {
		t.Errorf("limit = %q, want 20", got)
	}
	if got := seen.Header.Get("APCA-API-KEY-ID"); got != "mykey" {
		t.Errorf("key header = %q, want mykey", got)
	}
	if got := seen.Header.Get("APCA-API-SECRET-KEY"); got != "mysecret" {
		t.Errorf("secret header = %q, want mysecret", got)
	}
}

func TestIngest(t *testing.T) {
	srv, _ := newsServer(t, http.StatusOK, twoArticleFixture)
	c := New("k", "s")
	c.Base = srv.URL + "/v1beta1"
	c.HTTP = srv.Client()

	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", marketdata.Stocks, "Apple Inc.")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	// First ingest writes both articles.
	n, err := c.Ingest(ctx, st, sym.ID, "AAPL")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if n != 2 {
		t.Fatalf("first ingest count = %d, want 2", n)
	}

	stored, err := st.SymbolNews(ctx, sym.ID, 10)
	if err != nil {
		t.Fatalf("SymbolNews: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored %d rows, want 2", len(stored))
	}
	// SymbolID stamped by Ingest; headline/id/source persisted; sentiment
	// defaulted to 'unrated' by the schema.
	byID := map[string]store.NewsItem{}
	for _, s := range stored {
		byID[s.ID] = s
	}
	got, ok := byID["24843201"]
	if !ok {
		t.Fatal("article 24843201 not stored")
	}
	if got.Headline != "Apple Unveils New Chip" {
		t.Errorf("headline = %q", got.Headline)
	}
	if got.Source != "benzinga" {
		t.Errorf("source = %q", got.Source)
	}
	if got.Sentiment != "unrated" {
		t.Errorf("sentiment = %q, want unrated", got.Sentiment)
	}

	// Re-running is idempotent: INSERT OR IGNORE dedups on the provider id, so
	// no duplicate rows appear.
	n2, err := c.Ingest(ctx, st, sym.ID, "AAPL")
	if err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	if n2 != 2 {
		t.Fatalf("second ingest count = %d, want 2 (fetched-or-seen)", n2)
	}
	stored2, err := st.SymbolNews(ctx, sym.ID, 10)
	if err != nil {
		t.Fatalf("SymbolNews after re-run: %v", err)
	}
	if len(stored2) != 2 {
		t.Fatalf("after re-run stored %d rows, want 2 (idempotent)", len(stored2))
	}
}

// TestIngestFetchError surfaces fetch failures without writing anything.
func TestIngestFetchError(t *testing.T) {
	srv, _ := newsServer(t, http.StatusInternalServerError, `{"message":"boom"}`)
	c := New("k", "s")
	c.Base = srv.URL + "/v1beta1"
	c.HTTP = srv.Client()

	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", marketdata.Stocks, "Apple Inc.")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	n, err := c.Ingest(ctx, st, sym.ID, "AAPL")
	if err == nil {
		t.Fatal("expected error from failing fetch")
	}
	if n != 0 {
		t.Errorf("count = %d, want 0 on fetch error", n)
	}
	stored, err := st.SymbolNews(ctx, sym.ID, 10)
	if err != nil {
		t.Fatalf("SymbolNews: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("stored %d rows, want 0 on fetch error", len(stored))
	}
}

// TestFixtureIDsAreNumeric guards the "id is a NUMBER on the wire" assumption:
// the raw fixture must decode into an int64 id, which Fetch stringifies.
func TestFixtureIDsAreNumeric(t *testing.T) {
	var page newsPage
	if err := json.Unmarshal([]byte(twoArticleFixture), &page); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(page.News) != 2 {
		t.Fatalf("fixture has %d articles, want 2", len(page.News))
	}
	if page.News[0].ID != 24843201 {
		t.Errorf("first id = %d, want 24843201", page.News[0].ID)
	}
}
