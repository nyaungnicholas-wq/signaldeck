// LAYER 3 tests — the data boundary.
//
// The most important test in this file is TestEveryToolPayloadIsFullyAllowlisted:
// it runs every tool for real and fails if ANY field it emits lacks an
// allowlist entry. That is what turns the allowlist from documentation into a
// build gate — a new field either gets an entry or the build goes red.
package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnUnlistedFieldIsDropped(t *testing.T) {
	in := map[string]any{
		"topic":          "survivorship",
		"disclaimer":     "x",
		"internalRowID":  99,
		"symbolUniverse": []any{"AAPL", "MSFT"},
		"lastClose":      193.4,
		"databasePath":   "/data/signaldeck.db",
	}
	out, dropped := Sanitize("explain_methodology", in)
	for _, k := range []string{"internalRowID", "symbolUniverse", "lastClose", "databasePath"} {
		if _, ok := out[k]; ok {
			t.Errorf("unlisted field %q survived", k)
		}
	}
	if out["topic"] != "survivorship" {
		t.Error("an allowlisted field was dropped")
	}
	if len(dropped) != 4 {
		t.Errorf("expected 4 drops, got %v", dropped)
	}
}

func TestARawBarCannotBeRepresented(t *testing.T) {
	// Even at an allowlisted path, an object with the shape of a bar is refused.
	in := map[string]any{
		"verdicts": []any{map[string]any{
			"kind": "trend21", "open": 1.0, "high": 2.0, "low": 0.5, "close": 1.5, "volume": 100.0,
		}},
		"disclaimer": "x",
	}
	out, dropped := Sanitize("get_regime_verdict", in)
	blob, _ := json.Marshal(out)
	for _, f := range []string{"open", "high", "low", "close", "volume", "1.5"} {
		if strings.Contains(string(blob), f) {
			t.Fatalf("bar field %q reached the response: %s", f, blob)
		}
	}
	if len(dropped) == 0 {
		t.Fatal("a bar-shaped object was passed silently")
	}
}

func TestASeriesCannotBeRepresented(t *testing.T) {
	series := make([]any, 0, 40)
	for i := 0; i < 40; i++ {
		series = append(series, float64(i))
	}
	in := map[string]any{"verdicts": series, "disclaimer": "x"}
	out, dropped := Sanitize("get_regime_verdict", in)
	if _, ok := out["verdicts"]; ok {
		t.Fatal("a numeric series survived")
	}
	if len(dropped) == 0 {
		t.Fatal("no drop recorded for the series")
	}
}

func TestAGoStructIsDroppedWholesale(t *testing.T) {
	// This is the property that makes a store row unrepresentable rather than
	// merely unlikely: handing the serializer a typed value does not work.
	in := map[string]any{
		"verdicts":   Verdict{Symbol: "AAPL", Conviction: 0.93, HistoricalAccuracy: 0.97},
		"disclaimer": "x",
	}
	out, dropped := Sanitize("get_regime_verdict", in)
	if _, ok := out["verdicts"]; ok {
		t.Fatal("a struct reached the response path")
	}
	if len(dropped) == 0 {
		t.Fatal("no drop recorded for the struct")
	}
}

func TestLicensedAndRestrictedSourcesCannotReachAResponse(t *testing.T) {
	for _, s := range []string{
		"bars supplied by Alpaca under agreement",
		"sourced from Kraken exchange OHLC",
		"per the Coinbase live book",
		"TradingView scanner ratings",
		"StockTwits sentiment stream",
		"hyperliquid perp data",
		"cryptohist backfill",
	} {
		if !mentionsProtectedSource(s) {
			t.Errorf("a Licensed/Restricted provider was not detected in %q", s)
		}
		out, dropped := Sanitize("explain_methodology", map[string]any{"summary": s})
		if _, ok := out["summary"]; ok {
			t.Errorf("text naming a protected source survived: %q", s)
		}
		if len(dropped) == 0 {
			t.Errorf("no drop recorded for %q", s)
		}
	}
	// Public sources are fine to name — the boundary is the license class, not
	// the word "data".
	for _, s := range []string{"SEC EDGAR filings", "FRED series", "FINRA disclosure",
		"the market structure question", "measured in this market"} {
		if mentionsProtectedSource(s) {
			t.Errorf("false positive on public/ordinary text: %q", s)
		}
	}
}

func TestAnUnknownToolYieldsAnEmptyResponse(t *testing.T) {
	out, dropped := Sanitize("get_bars", map[string]any{"close": 1.0, "disclaimer": "x"})
	if len(out) != 0 {
		t.Fatalf("an unknown tool produced a payload: %v", out)
	}
	if len(dropped) == 0 {
		t.Fatal("no refusal recorded")
	}
}

// TestEveryToolPayloadIsFullyAllowlisted is the build gate. It runs every tool
// against a populated source and asserts that Sanitize dropped nothing, which
// means every field each tool emits has an explicit allowlist entry.
func TestEveryToolPayloadIsFullyAllowlisted(t *testing.T) {
	src := stubSource{
		verdicts: sampleVerdicts(),
		health: map[string]string{
			"directional-ensemble-1d": `{"model":"directional-ensemble-1d","verdict":"retired",` +
				`"emitting":false,"accuracy":0.467,"baseline":0.52,"observations":8191}`,
		},
		prereg: PreregSummary{
			ChainVerified: true, RegisteredBefore: true, FirstGradableOn: "2026-08-07",
			Claims: []PreregClaim{{Kind: "trend21", Question: "q", Baseline: "persistence",
				HorizonDays: 21, TopBandClaim: 0.972, RegisteredOn: "2026-07-26", SpecHash: "abc"}},
		},
	}
	s := newTestServer(t, Options{ReachablePrivately: true}, src)
	cases := []struct {
		tool string
		args toolArgs
	}{
		{"explain_methodology", toolArgs{Topic: "conviction_banding"}},
		{"critique_research_design", toolArgs{Description: "rolling daily sample of current index members compared against 50%"}},
		{"get_regime_verdict", toolArgs{Symbol: "AAPL"}},
		{"get_track_record", toolArgs{}},
		{"list_validated_findings", toolArgs{}},
		{"get_preregistration", toolArgs{}},
	}
	for _, c := range cases {
		payload, err := toolByName[c.tool].Run(context.Background(), s, fullClient(), c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		_, dropped := Sanitize(c.tool, payload)
		if len(dropped) > 0 {
			t.Errorf("%s emitted fields with no allowlist entry: %v\n"+
				"Add an entry to responseFields (allowlist.go) — deliberately, after checking the "+
				"field cannot carry a raw or licensed record.", c.tool, dropped)
		}
	}
	// And every topic, since each renders a different body.
	for _, topic := range methodologyTopics {
		payload, _ := toolByName["explain_methodology"].Run(context.Background(), s, fullClient(),
			toolArgs{Topic: topic})
		if _, dropped := Sanitize("explain_methodology", payload); len(dropped) > 0 {
			t.Errorf("topic %s dropped %v", topic, dropped)
		}
	}
}

// TestEveryResponseCarriesADisclaimer is the other half: not just that nothing
// forbidden gets out, but that nothing required gets left behind.
func TestEveryResponseCarriesADisclaimer(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{verdicts: sampleVerdicts()})
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"explain_methodology", map[string]any{"topic": "walk_forward"}},
		{"critique_research_design", map[string]any{"description": "a plain description of a study with a rolling window"}},
		{"get_regime_verdict", map[string]any{"symbol": "AAPL"}},
		{"get_track_record", map[string]any{}},
		{"list_validated_findings", map[string]any{}},
		{"get_preregistration", map[string]any{}},
	} {
		out, rerr := call(t, s, fullClient(), c.tool, c.args)
		if rerr != nil {
			t.Fatalf("%s: %v", c.tool, rerr)
		}
		d, _ := out["disclaimer"].(string)
		if !strings.Contains(d, "not investment advice") {
			t.Errorf("%s response carries no disclaimer", c.tool)
		}
	}
}

// TestNoResponseContainsARecommendation checks the advisory shape structurally
// rather than by reading the code: no response may contain the vocabulary of a
// trade instruction.
func TestNoResponseContainsARecommendation(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{verdicts: sampleVerdicts()})
	banned := []string{"\"buy\"", "\"sell\"", "targetPrice", "entryPrice", "exitPrice",
		"stopLoss", "positionSize", "\"recommendation\"", "\"signal\"", "allocation"}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"get_regime_verdict", map[string]any{"symbol": "AAPL"}},
		{"get_track_record", map[string]any{}},
		{"list_validated_findings", map[string]any{}},
		{"explain_methodology", map[string]any{"topic": "conviction_banding"}},
	} {
		out, rerr := call(t, s, fullClient(), c.tool, c.args)
		if rerr != nil {
			t.Fatalf("%s: %v", c.tool, rerr)
		}
		blob, _ := json.Marshal(out)
		for _, b := range banned {
			if strings.Contains(string(blob), b) {
				t.Errorf("%s response contains %q", c.tool, b)
			}
		}
	}
}

// TestAVerdictQuotesABandNotARawConviction — the raw conviction is a
// per-symbol continuous number and therefore a better extraction channel than
// the band it falls in, so it must never be serialized.
func TestAVerdictQuotesABandNotARawConviction(t *testing.T) {
	s := newTestServer(t, Options{ReachablePrivately: true}, stubSource{verdicts: sampleVerdicts()})
	out, rerr := call(t, s, fullClient(), "get_regime_verdict", map[string]any{"symbol": "AAPL"})
	if rerr != nil {
		t.Fatalf("%v", rerr)
	}
	blob, _ := json.Marshal(out)
	if strings.Contains(string(blob), "0.93") || strings.Contains(string(blob), "conviction\":0") {
		t.Fatalf("a raw conviction was serialized: %s", blob)
	}
	if !strings.Contains(string(blob), "very-high") {
		t.Fatalf("no conviction band in the verdict: %s", blob)
	}
	// Every number must travel with its sample size and caveat.
	for _, need := range []string{"bandSampleN", "bandAccuracy", "evidenceCaveat", "firstGradableOn"} {
		if !strings.Contains(string(blob), need) {
			t.Errorf("verdict is missing %s — a bare accuracy figure is a bug", need)
		}
	}
}
