package store

import (
	"context"
	"testing"
)

// A re-applied epoch schedule must CORRECT a boundary, never append a second
// one. Two rows for the same epoch would give CurrentPaperEpoch a choice, and
// the published numbers would depend on which it happened to pick.
func TestUpsertPaperEpoch_IsIdempotentByStrategyAndEpoch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	first := PaperEpoch{
		Strategy: "alpha",
		Epoch:    1,
		FromTs:   1000,
		Label:    "v1",
		Reason:   "launch",
	}
	if err := s.UpsertPaperEpoch(ctx, first); err != nil {
		t.Fatalf("first upsert err = %v, want nil", err)
	}
	second := PaperEpoch{
		Strategy: "alpha",
		Epoch:    1,
		FromTs:   1500,
		Label:    "v1-fixed",
		Reason:   "corrected boundary",
	}
	if err := s.UpsertPaperEpoch(ctx, second); err != nil {
		t.Fatalf("second upsert err = %v, want nil", err)
	}
	epochs, err := s.PaperEpochs(ctx, "alpha")
	if err != nil {
		t.Fatalf("PaperEpochs err = %v, want nil", err)
	}
	if got := len(epochs); got != 1 {
		t.Fatalf("got = %v, want %v", got, 1)
	}
	e := epochs[0]
	if e.FromTs != 1500 {
		t.Fatalf("got.FromTs = %v, want %v", e.FromTs, 1500)
	}
	if e.Label != "v1-fixed" {
		t.Fatalf("got.Label = %v, want %v", e.Label, "v1-fixed")
	}
	if e.Reason != "corrected boundary" {
		t.Fatalf("got.Reason = %v, want %v", e.Reason, "corrected boundary")
	}
}

// Epochs are scoped to one strategy and ordered by epoch number, not by
// insertion. Inserted 3,1,2 on purpose: written in order, the ORDER BY would be
// indistinguishable from insertion order and the test would pin nothing.
// EpochBounds reads this list positionally, so a wrong order silently pairs
// each epoch with the wrong end.
func TestPaperEpochs_ScopedToStrategyAndOrderedByEpoch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, ep := range []PaperEpoch{
		{Strategy: "alpha", Epoch: 3, FromTs: 3000, Label: "c", Reason: ""},
		{Strategy: "alpha", Epoch: 1, FromTs: 1000, Label: "a", Reason: ""},
		{Strategy: "alpha", Epoch: 2, FromTs: 2000, Label: "b", Reason: ""},
	} {
		if err := s.UpsertPaperEpoch(ctx, ep); err != nil {
			t.Fatalf("upsert err = %v, want nil", err)
		}
	}
	if err := s.UpsertPaperEpoch(ctx, PaperEpoch{Strategy: "beta", Epoch: 1, FromTs: 500, Label: "x", Reason: ""}); err != nil {
		t.Fatalf("beta upsert err = %v, want nil", err)
	}
	epochs, err := s.PaperEpochs(ctx, "alpha")
	if err != nil {
		t.Fatalf("PaperEpochs err = %v, want nil", err)
	}
	if got := len(epochs); got != 3 {
		t.Fatalf("got = %v, want %v", got, 3)
	}
	want := []int{1, 2, 3}
	for i, ep := range epochs {
		if ep.Strategy != "alpha" {
			t.Fatalf("epoch %d Strategy = %v, want alpha", i, ep.Strategy)
		}
		if ep.Epoch != want[i] {
			t.Fatalf("epoch %d Epoch = %v, want %v", i, ep.Epoch, want[i])
		}
	}
	beta, err := s.PaperEpochs(ctx, "beta")
	if err != nil {
		t.Fatalf("PaperEpochs beta err = %v, want nil", err)
	}
	if got := len(beta); got != 1 {
		t.Fatalf("beta epochs got = %v, want %v", got, 1)
	}
}

// An undeclared strategy is empty, not an error: a caller must be able to tell
// "no epochs declared" from "the lookup failed" without inspecting an error
// string.
func TestPaperEpochs_UnknownStrategyIsEmptyNotAnError(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	epochs, err := s.PaperEpochs(ctx, "nope")
	if err != nil {
		t.Fatalf("PaperEpochs err = %v, want nil", err)
	}
	if got := len(epochs); got != 0 {
		t.Fatalf("got = %v, want %v", got, 0)
	}
}

// The boundary itself belongs to the NEW epoch (from_ts is inclusive), and a
// timestamp before every boundary yields ok=false with a ZERO PaperEpoch.
// Callers must read that as "do not scope" -- silently inventing an epoch
// starting at 0 is how a filter meant to protect a measurement quietly stops
// filtering anything.
func TestCurrentPaperEpoch_PicksTheNewestBoundaryAtOrBefore(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, ep := range []PaperEpoch{
		{Strategy: "alpha", Epoch: 1, FromTs: 1000, Label: "", Reason: ""},
		{Strategy: "alpha", Epoch: 2, FromTs: 2000, Label: "", Reason: ""},
		{Strategy: "alpha", Epoch: 3, FromTs: 3000, Label: "", Reason: ""},
	} {
		if err := s.UpsertPaperEpoch(ctx, ep); err != nil {
			t.Fatalf("upsert err = %v, want nil", err)
		}
	}
	tests := []struct {
		at   int64
		want int
		ok   bool
	}{
		{2500, 2, true},
		{2000, 2, true},
		{999, 0, false},
		{99999, 3, true},
	}
	for _, tt := range tests {
		got, ok, err := s.CurrentPaperEpoch(ctx, "alpha", tt.at)
		if err != nil {
			t.Fatalf("CurrentPaperEpoch err = %v, want nil", err)
		}
		if ok != tt.ok {
			t.Fatalf("at=%d: got ok=%v, want %v", tt.at, ok, tt.ok)
		}
		if ok {
			if got.Epoch != tt.want {
				t.Fatalf("at=%d: got Epoch=%v, want %v", tt.at, got.Epoch, tt.want)
			}
			if got.Strategy != "alpha" {
				t.Fatalf("at=%d: got Strategy=%v, want alpha", tt.at, got.Strategy)
			}
		} else {
			if got.Epoch != 0 || got.FromTs != 0 || got.Strategy != "" || got.Label != "" || got.Reason != "" {
				t.Fatalf("at=%d: got epoch=%#v, want zero PaperEpoch", tt.at, got)
			}
		}
	}
}

// The newest epoch is open-ended (to=0) because it has no successor yet, and an
// out-of-range index returns (0,0) rather than panicking on a caller that
// walked past the end of the list.
func TestEpochBounds_LastEpochIsOpenAndOutOfRangeIsZero(t *testing.T) {
	epochs := []PaperEpoch{
		{Epoch: 1, FromTs: 0},
		{Epoch: 2, FromTs: 2000},
		{Epoch: 3, FromTs: 3000},
	}
	testCases := []struct {
		i    int
		from int64
		to   int64
	}{
		{0, 0, 2000},
		{1, 2000, 3000},
		{2, 3000, 0},
		{-1, 0, 0},
		{3, 0, 0},
	}
	for _, tt := range testCases {
		gotFrom, gotTo := EpochBounds(epochs, tt.i)
		if gotFrom != tt.from || gotTo != tt.to {
			t.Fatalf("EpochBounds(epochs, %d) = (%d,%d), want (%d,%d)", tt.i, gotFrom, gotTo, tt.from, tt.to)
		}
	}
	if gotFrom, gotTo := EpochBounds(nil, 0); gotFrom != 0 || gotTo != 0 {
		t.Fatalf("EpochBounds(nil,0) = (%d,%d), want (0,0)", gotFrom, gotTo)
	}
}

// The window is half-open [from, to). ts == to is OUTSIDE: a mark exactly on a
// boundary belongs to the next epoch, so no row is counted in two epochs and
// none falls between them.
func TestInEpoch_IsHalfOpen(t *testing.T) {
	tests := []struct {
		ts   int64
		from int64
		to   int64
		want bool
	}{
		{500, 1000, 2000, false},
		{1000, 1000, 2000, true},
		{1500, 1000, 2000, true},
		{2000, 1000, 2000, false},
		{2500, 1000, 2000, false},
		{5000, 1000, 0, true},
		{500, 1000, 0, false},
	}
	for _, tt := range tests {
		if got := InEpoch(tt.ts, tt.from, tt.to); got != tt.want {
			t.Fatalf("InEpoch(%d,%d,%d) = %v, want %v", tt.ts, tt.from, tt.to, got, tt.want)
		}
	}
}
