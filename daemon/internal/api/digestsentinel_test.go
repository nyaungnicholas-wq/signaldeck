package api

import (
	"context"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/memberdigest"
)

// TestDigestCarriesNoVendorSentinels holds the member daily email to the same
// licence line as the member routes: composed from a store seeded with every
// vendor sentinel (seedSentinels), for a member watching a stock with bars and
// a crypto pair, the email carries derived forecasts only and no crypto.
func TestDigestCarriesNoVendorSentinels(t *testing.T) {
	_, st, _, _ := newProductionServer(t, nil, writeRegistry(t, thinWindowRegistry))
	fx := seedSentinels(t, st, 0)
	facts, err := memberdigest.LoadFacts(context.Background(), st, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d, ok := memberdigest.Compose(facts, []md.Symbol{fx.sntl, fx.sntc, fx.sntw},
		"https://sd.example", "https://sd.example/api/alerts/unsubscribe?token=x")
	if !ok {
		t.Fatal("no digest composed for a watchlist with stocks")
	}
	text := d.Subject + "\n" + d.Body
	// Positive control: the email is built from the seeded forecast rows.
	for _, want := range []string{"SNTL\n", "trend21: uptrend", "vol63: elevated", "SNTW\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("digest missing %q, so it is not reading the seeded store:\n%s", want, text)
		}
	}
	for _, l := range leaks(text, vendorSentinels()) {
		t.Errorf("member digest leaks a vendor value (%s):\n%s", l, text)
	}
	if strings.Contains(text, "SNTC") {
		t.Errorf("member digest carries a crypto symbol:\n%s", text)
	}
}
