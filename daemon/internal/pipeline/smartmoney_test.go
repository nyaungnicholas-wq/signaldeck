package pipeline

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/smartmoney"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The FINRA short-volume poller was retired 22 Oct 2026. A stored series whose
// newest day is older than shortVolMaxAgeDays must drop out of the Squeeze
// sub-part exactly like a missing input; a fresh series still counts.
func TestSmartMoneyShortVolFreshnessGate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "sm_fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	ids := map[string]int64{}
	for sym, lag := range map[string]int{"FRESH": 1, "STALE": shortVolMaxAgeDays + 5} {
		s, err := st.UpsertSymbol(ctx, sym, md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		ids[sym] = s.ID
		var rows []store.ShortVolumeRow
		for d := 0; d < 20; d++ {
			pct := 0.30 + float64(d%5)/100
			if d == 0 {
				pct = 0.60 // the newest day spikes, so a fresh z clears the gate
			}
			rows = append(rows, store.ShortVolumeRow{SymbolID: s.ID, ShortVol: pct * 100, TotalVol: 100,
				ShortPct: pct, Day: now.AddDate(0, 0, -(lag + d)).Format("2006-01-02")})
		}
		if err := st.UpsertShortVolume(ctx, rows); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (&SmartMoneyScorer{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	row, ok, err := st.LatestSmartMoneyScore(ctx, ids["FRESH"])
	var p smartmoney.Payload
	if err != nil || !ok || json.Unmarshal([]byte(row.Payload), &p) != nil || p.ShortVolZ == nil {
		t.Fatalf("fresh series must feed shortVolZ: ok=%v err=%v payload=%s", ok, err, row.Payload)
	}
	if row, ok, err := st.LatestSmartMoneyScore(ctx, ids["STALE"]); err != nil || ok {
		t.Fatalf("stale short-volume series still scored: err=%v payload=%s", err, row.Payload)
	}
}
