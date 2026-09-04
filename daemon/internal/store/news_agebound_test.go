package store

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A headline older than yesterday can never reach a sentiment_daily aggregate,
// because the sentiment-aggregator only recomputes today and yesterday and
// nothing backfills older days, so tagging it spends the daily LLM budget
// writing a tag nothing can read.
func TestUnratedNews_MinTsBoundsTheQueue(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	recent := time.Now().Unix()
	ancient := time.Now().AddDate(0, 0, -400).Unix()
	addRatedNews(t, st, "recent", sym.ID, recent, "unrated", 0)
	addRatedNews(t, st, "ancient", sym.ID, ancient, "unrated", 0)

	got, err := st.UnratedNews(ctx, 10, 0)
	if err != nil {
		t.Fatalf("UnratedNews: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("minTs=0 returned %d rows, want 2 (0 means unbounded)", len(got))
	}

	cutoff := time.Now().AddDate(0, 0, -7).Unix()
	got, err = st.UnratedNews(ctx, 10, cutoff)
	if err != nil {
		t.Fatalf("UnratedNews: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("minTs=7d returned %d rows, want 1", len(got))
	}
	if got[0].Headline != "h recent" {
		t.Fatalf("returned %q, want the recent headline", got[0].Headline)
	}
}