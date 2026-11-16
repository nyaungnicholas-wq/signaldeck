package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/wikimedia"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// With MaxPerRun below the scope, successive passes rotate through every
// symbol instead of revisiting the first MaxPerRun forever (audit 11-10).
func TestWikiAttentionRotatesThroughScope(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()
	var ids []int64
	var companies []store.CompanyRow
	for i, sym := range []string{"AAA", "BBB", "CCC"} {
		s, _ := st.UpsertSymbol(ctx, sym, md.Stocks, "")
		_ = st.SetSymbolStream(ctx, s.ID, true)
		ids = append(ids, s.ID)
		companies = append(companies, store.CompanyRow{CIK: int64(100 + i), Ticker: sym, Name: sym + " Corp", UpdatedTs: 1})
	}
	if err := st.UpsertCompanies(ctx, companies); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"article":"x","timestamp":"2026070900","views":10}]}`))
	}))
	defer srv.Close()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	w := &WikiAttention{St: st, MaxPerRun: 1,
		Client: &wikimedia.Client{BaseURL: srv.URL, MinInterval: time.Millisecond},
		Now:    func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		if _, err := w.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range ids {
		if _, found, _ := st.GetWikiArticle(ctx, id); !found {
			t.Fatalf("symbol %d never visited in 3 passes of MaxPerRun=1", i)
		}
	}
}
