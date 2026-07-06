package api

// STAGE 2 — pinned weekly signal-backtest retrieval: ?pinned=1 serves the
// stored Sunday snapshot verbatim; when no pin exists the handler falls back
// to a live compute and says so; plain requests stay live and unlabeled.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/signalbt"
)

// signalBTPinnedBody decodes the Stage-2 additions on the payload.
type signalBTPinnedBody struct {
	Result struct {
		Horizon      string  `json:"horizon"`
		IndependentN int     `json:"independentN"`
		Gated        bool    `json:"gated"`
		IC           float64 `json:"ic"`
	} `json:"result"`
	HasBenchmark bool   `json:"hasBenchmark"`
	Pinned       bool   `json:"pinned"`
	PinnedDay    string `json:"pinnedDay"`
	PinnedTs     int64  `json:"pinnedTs"`
	PinnedNote   string `json:"pinnedNote"`
}

func getSignalBTPinned(t *testing.T, url string) signalBTPinnedBody {
	t.Helper()
	res, err := newClient(t).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status=%d want 200", res.StatusCode)
	}
	var body signalBTPinnedBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSignalBacktestPinnedRetrieval(t *testing.T) {
	srv, st := newSignalBTServer(t, nil)
	ctx := context.Background()

	// A stored weekly pin for 1d only (fabricated snapshot — retrieval test).
	pin := signalbt.Pinned{
		DayKey: "2026-07-05", ComputedTs: 1751756400,
		BenchmarkSymbol: "SPY", HasBenchmark: true,
		Results: map[string]signalbt.Result{
			"1d": {Horizon: "1d", RawN: 100, IndependentN: 42, MinIndependentN: 30, Gated: false, IC: 0.05},
		},
	}
	if err := st.SetJSON(ctx, signalbt.MetaKeyLatest, pin); err != nil {
		t.Fatal(err)
	}

	// pinned=1 for a stored horizon → the snapshot verbatim, labeled pinned.
	got := getSignalBTPinned(t, srv.URL+"/api/signal-backtest?horizon=1d&pinned=1")
	if !got.Pinned || got.PinnedDay != "2026-07-05" || got.PinnedTs != 1751756400 {
		t.Fatalf("pinned envelope = %+v", got)
	}
	if got.Result.IndependentN != 42 || got.Result.IC != 0.05 || got.Result.Gated {
		t.Fatalf("pinned result not served verbatim: %+v", got.Result)
	}

	// pinned=1 for a horizon the snapshot lacks → honest live fallback + note.
	got = getSignalBTPinned(t, srv.URL+"/api/signal-backtest?horizon=1w&pinned=1")
	if got.Pinned {
		t.Fatal("1w is not in the pin — must fall back to live compute")
	}
	if got.PinnedNote == "" {
		t.Fatal("live fallback must say why it is not pinned")
	}
	if got.Result.Horizon != "1w" {
		t.Fatalf("fallback horizon = %q want 1w", got.Result.Horizon)
	}

	// A plain live request stays unlabeled (pinned=false, no note).
	got = getSignalBTPinned(t, srv.URL+"/api/signal-backtest?horizon=1d")
	if got.Pinned || got.PinnedNote != "" {
		t.Fatalf("live request mislabeled: pinned=%v note=%q", got.Pinned, got.PinnedNote)
	}
}

// TestSignalBacktestPinnedAbsent covers the fresh-install path: pinned=1 with
// no stored snapshot at all → live compute + the explanatory note.
func TestSignalBacktestPinnedAbsent(t *testing.T) {
	srv, _ := newSignalBTServer(t, nil)
	got := getSignalBTPinned(t, srv.URL+"/api/signal-backtest?horizon=1d&pinned=1")
	if got.Pinned {
		t.Fatal("no pin stored — response must not claim one")
	}
	if got.PinnedNote == "" {
		t.Fatal("missing pinnedNote on the no-pin fallback")
	}
}
