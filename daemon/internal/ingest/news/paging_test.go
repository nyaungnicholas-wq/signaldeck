package news

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Once a symbol has stored news, Ingest pages back until it meets it; a busy
// day of more than one page is no longer cut at the first 20 headlines.
func TestIngestPagesBackToStoredNews(t *testing.T) {
	article := func(id int, ts string) string {
		return fmt.Sprintf(`{"id":%d,"headline":"h%d","source":"s","url":"u","created_at":"%s","symbols":["AAPL"]}`, id, id, ts)
	}
	pages := map[string]string{
		"":   `{"news":[` + article(5, "2026-07-03T15:00:00Z") + `,` + article(4, "2026-07-03T14:00:00Z") + `],"next_page_token":"p2"}`,
		"p2": `{"news":[` + article(3, "2026-07-02T15:00:00Z") + `,` + article(2, "2026-07-01T10:00:00Z") + `],"next_page_token":"p3"}`,
		"p3": `{"news":[` + article(1, "2026-06-30T10:00:00Z") + `],"next_page_token":null}`,
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(pages[r.URL.Query().Get("page_token")]))
	}))
	t.Cleanup(srv.Close)
	c := New("k", "s")
	c.Base = srv.URL + "/v1beta1"
	c.HTTP = srv.Client()

	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", marketdata.Stocks, "Apple Inc.")
	if err != nil {
		t.Fatal(err)
	}
	// Stored history ends at article 2 (2026-07-01 10:00).
	if err := st.InsertNews(ctx, store.NewsItem{ID: "2", SymbolID: sym.ID, Ts: 1782900000, Headline: "h2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Ingest(ctx, st, sym.ID, "AAPL"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.SymbolNews(ctx, sym.ID, 10)
	if len(got) != 4 || calls != 2 {
		t.Fatalf("stored %d articles in %d calls, want 4 in 2 (articles 5,4,3 plus the stored one; stop at page 2)", len(got), calls)
	}
}
