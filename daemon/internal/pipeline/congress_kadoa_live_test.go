package pipeline

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/congress"
)

func TestCongressPollerKadoaFallbackLive(t *testing.T) {
	if os.Getenv("SIGNALDECK_LIVE_KADOA") != "1" {
		t.Skip("live Kadoa test skipped unless SIGNALDECK_LIVE_KADOA=1")
	}
	st := openCongressStore(t)
	ctx := context.Background()

	c := congress.New()
	c.SenateURL = "http://127.0.0.1:9/dead"
	c.HouseURL = "http://127.0.0.1:9/dead"

	w := &CongressPoller{St: st, Client: c, KadoaFallback: true}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !strings.Contains(detail, "senate") || !strings.Contains(detail, "house") {
		t.Fatalf("detail must contain both senate and house: %q", detail)
	}

	rows, err := st.CongressTrades(ctx, "", "", "", 0)
	if err != nil {
		t.Fatalf("CongressTrades: %v", err)
	}

	var senateCount, houseCount int
	for _, r := range rows {
		if r.Chamber == "senate" {
			senateCount++
		} else if r.Chamber == "house" {
			houseCount++
		}
	}

	if senateCount < 1 {
		t.Fatalf("expected at least one senate row, got %d", senateCount)
	}
	if houseCount < 1 {
		t.Fatalf("expected at least one house row, got %d", houseCount)
	}

	t.Logf("detail: %s", detail)
	t.Logf("senate rows: %d, house rows: %d", senateCount, houseCount)
}
