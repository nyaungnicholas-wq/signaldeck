package api

import (
	"strings"
	"testing"
)

// The directional surface must state that its outcomes are gross of costs, and
// must state it UNCONDITIONALLY.
//
// Before this, the no-costs fact lived in a Go package comment and in the halt
// path — which only fires once the model has already stopped emitting. A healthy
// forecast went out with nothing, i.e. exactly when a reader is most likely to
// act on it. A disclaimer that shows up only when something is broken teaches
// readers that its absence means safe.
func TestCompositeTradeabilityNamesEveryCost(t *testing.T) {
	for _, want := range []string{"cost", "spread", "slippage", "latency"} {
		if !strings.Contains(strings.ToLower(compositeTradeability), want) {
			t.Errorf("directional tradeability text does not mention %q", want)
		}
	}
	// It must refuse the trade outright, not merely hedge.
	if !strings.Contains(compositeTradeability, "NOT A TRADE") {
		t.Error("must lead with NOT A TRADE, matching the structural surface's wording")
	}
	// Accuracy-is-not-return is the finding that motivated the structural field;
	// the directional twin must carry it too.
	low := strings.ToLower(compositeTradeability)
	if !strings.Contains(low, "negative mean forward return") {
		t.Error("must carry the accuracy-is-not-return finding")
	}
	// A disclaimer nobody reads is decoration; keep it substantive.
	if len(compositeTradeability) < 200 {
		t.Errorf("tradeability text is %d chars — too thin to be honest", len(compositeTradeability))
	}
}

// The three notes served beside it must stay distinct: curve is a rank, edge is
// a probability offset, tradeability is the cost statement. Collapsing any two
// is how one of them silently stops being said.
func TestCompositeNotesAreDistinct(t *testing.T) {
	notes := map[string]string{
		"curve":        compositeCurveNote,
		"edge":         compositeEdgeNote,
		"tradeability": compositeTradeability,
	}
	for a, va := range notes {
		if strings.TrimSpace(va) == "" {
			t.Errorf("%s note is empty", a)
		}
		for b, vb := range notes {
			if a != b && va == vb {
				t.Errorf("%s and %s notes are identical", a, b)
			}
		}
	}
}
