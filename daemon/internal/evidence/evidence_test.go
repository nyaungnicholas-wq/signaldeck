package evidence

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "evidence.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// validClaim is a well-formed moderate claim fixture; tests mutate copies.
func validClaim() Claim {
	return Claim{
		ID:   "test-claim",
		Text: "test predictor beats its baseline",
		Scope: Scope{
			Assets: []string{"stocks"}, Horizons: []string{"1d"},
			DateFrom: "2026-07-01", DateTo: "2026-07-26",
		},
		Items: []Item{{
			Kind: "live-record", Value: 0.56, NEffective: 150,
			Method: "walk-forward",
			CILow:  f(0.53), CIHigh: f(0.61), Baseline: f(0.52),
			SourceRef: "test",
		}},
		Tier: TierModerate, Status: StatusActive,
		LastValidated: time.Now().Unix(),
		RevalidateBy:  time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
}

// ─── Validation rules ────────────────────────────────────────────────────

func TestClaimWithoutEvidenceIsRejected(t *testing.T) {
	c := validClaim()
	c.Items = nil
	if err := c.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("want ErrNoEvidence, got %v", err)
	}
}

func TestItemWithoutNEffectiveOrMethodIsRejected(t *testing.T) {
	c := validClaim()
	c.Items[0].NEffective = 0
	if err := c.Validate(); !errors.Is(err, ErrItemIncomplete) {
		t.Fatalf("missing n_effective: want ErrItemIncomplete, got %v", err)
	}
	c = validClaim()
	c.Items[0].Method = ""
	if err := c.Validate(); !errors.Is(err, ErrItemIncomplete) {
		t.Fatalf("missing method: want ErrItemIncomplete, got %v", err)
	}
}

// The core honesty rule: a tier is JUSTIFIED, not asserted. strong requires
// CI excluding the null AND a correction AND n_effective >= StrongNEff.
func TestStrongTierRequiresAllThreeConditions(t *testing.T) {
	base := validClaim()
	base.Items[0].NEffective = StrongNEff
	base.Items[0].Correction = "bonferroni"
	base.Tier = TierStrong
	if err := base.Validate(); err != nil {
		t.Fatalf("fully-justified strong should validate: %v", err)
	}

	noCorr := base
	noCorr.Items = []Item{base.Items[0]}
	noCorr.Items[0].Correction = ""
	if err := noCorr.Validate(); !errors.Is(err, ErrTierUnjustified) {
		t.Fatalf("strong without correction: want ErrTierUnjustified, got %v", err)
	}

	thin := base
	thin.Items = []Item{base.Items[0]}
	thin.Items[0].NEffective = StrongNEff - 1
	if err := thin.Validate(); !errors.Is(err, ErrTierUnjustified) {
		t.Fatalf("strong under n floor: want ErrTierUnjustified, got %v", err)
	}

	nullInside := base
	nullInside.Items = []Item{base.Items[0]}
	nullInside.Items[0].CILow = f(0.50) // baseline 0.52 inside the interval
	if err := nullInside.Validate(); !errors.Is(err, ErrTierUnjustified) {
		t.Fatalf("strong with null inside CI: want ErrTierUnjustified, got %v", err)
	}
}

func TestUnderclaimingIsAllowed(t *testing.T) {
	c := validClaim()
	c.Items[0].NEffective = StrongNEff
	c.Items[0].Correction = "bonferroni"
	c.Tier = TierWeak // justified strong, stated weak — humility is legal
	if err := c.Validate(); err != nil {
		t.Fatalf("underclaiming should validate: %v", err)
	}
}

func TestRefutingEvidenceForcesRefutedTier(t *testing.T) {
	c := validClaim()
	c.Items[0].CILow = f(0.44)
	c.Items[0].CIHigh = f(0.50) // entirely below baseline 0.52
	if err := c.Validate(); !errors.Is(err, ErrTierUnjustified) {
		t.Fatalf("refuted evidence with moderate tier: want ErrTierUnjustified, got %v", err)
	}
	c.Tier = TierRefuted
	if err := c.Validate(); err != nil {
		t.Fatalf("refuted claim stated refuted should validate: %v", err)
	}
}

func TestNoIntervalJustifiesOnlyWeak(t *testing.T) {
	c := validClaim()
	c.Items[0].CILow, c.Items[0].CIHigh = nil, nil
	if got := JustifiedTier(c.Items); got != TierWeak {
		t.Fatalf("no interval: justified %q, want weak", got)
	}
}

// ─── Staleness sweep ─────────────────────────────────────────────────────

func TestSweepDowngradesPastDueClaims(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := validClaim()
	c.RevalidateBy = time.Now().Add(-24 * time.Hour).Unix()
	if err := Put(ctx, st, c); err != nil {
		t.Fatalf("put: %v", err)
	}
	res, err := Sweep(ctx, st, time.Now())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Staled != 1 {
		t.Fatalf("staled=%d, want 1", res.Staled)
	}
	got, err := Get(ctx, st, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusStale || got.Tier != TierWeak {
		t.Fatalf("after sweep: status=%q tier=%q, want stale/weak", got.Status, got.Tier)
	}
	// A second pass must NOT downgrade again — one tier per missed deadline.
	if _, err := Sweep(ctx, st, time.Now()); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	got, _ = Get(ctx, st, c.ID)
	if got.Tier != TierWeak {
		t.Fatalf("second sweep re-downgraded: tier=%q", got.Tier)
	}
}

func TestSweepLeavesFreshClaimsAlone(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := Put(ctx, st, validClaim()); err != nil {
		t.Fatalf("put: %v", err)
	}
	res, err := Sweep(ctx, st, time.Now())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Staled != 0 || res.Retired != 0 {
		t.Fatalf("fresh claim touched: %+v", res)
	}
}

func TestSweepRetiresRefutedClaims(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := validClaim()
	c.Items[0].CILow, c.Items[0].CIHigh = f(0.44), f(0.50)
	c.Tier = TierRefuted
	if err := Put(ctx, st, c); err != nil {
		t.Fatalf("put: %v", err)
	}
	res, err := Sweep(ctx, st, time.Now())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Retired != 1 {
		t.Fatalf("retired=%d, want 1", res.Retired)
	}
	got, _ := Get(ctx, st, c.ID)
	if got.Status != StatusRetired || got.Tier != TierRefuted {
		t.Fatalf("after sweep: status=%q tier=%q, want retired/refuted", got.Status, got.Tier)
	}
}

// ─── Round-trip through the store ────────────────────────────────────────

func TestRoundTripPreservesEveryField(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := validClaim()
	c.Items[0].Correction = "bonferroni"
	c.Lineage = Lineage{FeatureKeys: []string{"trend21"}, Models: []string{"structural-regime"}}
	c.Seeded = true
	if err := Put(ctx, st, c); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := Get(ctx, st, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Text != c.Text || got.Tier != c.Tier || got.Status != c.Status ||
		got.LastValidated != c.LastValidated || got.RevalidateBy != c.RevalidateBy ||
		!got.Seeded || len(got.Items) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	it, want := got.Items[0], c.Items[0]
	if it.Kind != want.Kind || it.Value != want.Value || it.NEffective != want.NEffective ||
		it.Method != want.Method || it.Correction != want.Correction ||
		*it.CILow != *want.CILow || *it.CIHigh != *want.CIHigh || *it.Baseline != *want.Baseline ||
		it.SourceRef != want.SourceRef {
		t.Fatalf("item round-trip mismatch: %+v vs %+v", it, want)
	}
	if len(got.Lineage.FeatureKeys) != 1 || got.Lineage.FeatureKeys[0] != "trend21" {
		t.Fatalf("lineage lost: %+v", got.Lineage)
	}
}

func TestPutRejectsInvalidClaims(t *testing.T) {
	st := newTestStore(t)
	c := validClaim()
	c.Items = nil
	if err := Put(context.Background(), st, c); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("invalid claim reached Put: %v", err)
	}
}

// ─── Seeds ───────────────────────────────────────────────────────────────

// Every shipped seed must pass the engine's own validation — the seeds ARE
// the executable examples.
func TestSeedClaimsAllValidate(t *testing.T) {
	for _, c := range SeedClaims() {
		if err := c.Validate(); err != nil {
			t.Errorf("seed %s: %v", c.ID, err)
		}
		if !c.Seeded {
			t.Errorf("seed %s not marked seeded", c.ID)
		}
	}
}

func TestEnsureSeedsIsIdempotentAndNeverOverwrites(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, err := EnsureSeeds(ctx, st)
	if err != nil || n != len(SeedClaims()) {
		t.Fatalf("first seed: n=%d err=%v", n, err)
	}
	// Sweep may change state; re-seeding must not resurrect it.
	if err := st.SetEvidenceClaimState(ctx, "trend21-structural", string(StatusStale), string(TierWeak)); err != nil {
		t.Fatalf("set state: %v", err)
	}
	n, err = EnsureSeeds(ctx, st)
	if err != nil || n != 0 {
		t.Fatalf("second seed: n=%d err=%v", n, err)
	}
	got, _ := Get(ctx, st, "trend21-structural")
	if got.Status != StatusStale {
		t.Fatalf("re-seed overwrote swept state: %q", got.Status)
	}
}

// The retired 1d claim's numbers must actually refute under the engine's own
// rules — the seed is a regression test on the audit's headline finding.
func TestSeededDirectional1dIsRefutedByItsOwnNumbers(t *testing.T) {
	for _, c := range SeedClaims() {
		if c.ID != "directional-ensemble-1d" {
			continue
		}
		if got := JustifiedTier(c.Items); got != TierRefuted {
			t.Fatalf("1d seed justified %q, want refuted", got)
		}
		return
	}
	t.Fatal("directional-ensemble-1d seed missing")
}
