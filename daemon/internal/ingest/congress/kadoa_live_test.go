package congress

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestKadoaLiveFetch proves the fallback against the live shape rather than a fixture that may drift.
func TestKadoaLiveFetch(t *testing.T) {
	if os.Getenv("SIGNALDECK_LIVE_KADOA") != "1" {
		t.Skip("set SIGNALDECK_LIVE_KADOA=1 to fetch the live feed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := New()
	for _, ch := range []string{ChamberSenate, ChamberHouse} {
		trades, err := c.FetchKadoa(ctx, ch)
		if err != nil {
			t.Fatalf("FetchKadoa %s: %v", ch, err)
		}
		if len(trades) == 0 {
			t.Fatalf("live feed returned zero usable trades for %s", ch)
		}
		tr := trades[0]
		if tr.Chamber != ch || tr.Ticker == "" || tr.Member == "" || tr.ID == "" || tr.DisclosedTs == 0 {
			t.Fatalf("unexpected trade shape for %s: %+v", ch, tr)
		}
		t.Logf("%s: %d trades; newest disclosed %s (%s %s %s)", ch, len(trades), time.Unix(tr.DisclosedTs, 0).UTC().Format("2006-01-02"), tr.Member, tr.TxType, tr.Ticker)
	}
}
