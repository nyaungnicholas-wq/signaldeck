package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/featurehealth"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// These tests are about ENFORCEMENT. internal/featurehealth's own tests prove the
// grading; what has to be proven here is that a retired feature actually leaves
// the training vector, and that the failure modes fail in the safe direction.

// A retired base feature must take its presence indicator with it. Dropping only
// the base value would leave an orphaned column that is always 1 and means
// nothing.
func TestModelFeatureKeysExcludingDropsPresencePairs(t *testing.T) {
	labeled := []store.LabeledFeature{
		{Vec: map[string]float64{"alpha": 1, "beta": 2, "gamma": 3}},
		{Vec: map[string]float64{"alpha": 4, "beta": 5, "gamma": 6}},
	}

	full := modelFeatureKeysExcluding(labeled, nil)
	if !containsStr(full, "beta") || !containsStr(full, "beta"+presenceSuffix) {
		t.Fatalf("fixture assumption broken: expected beta and its presence bit in %v", full)
	}

	filtered := modelFeatureKeysExcluding(labeled, map[string]bool{"beta": true})
	if containsStr(filtered, "beta") {
		t.Error("a retired feature must leave the training vector")
	}
	if containsStr(filtered, "beta"+presenceSuffix) {
		t.Error("a retired feature's presence indicator must leave with it")
	}
	if !containsStr(filtered, "alpha") || !containsStr(filtered, "gamma"+presenceSuffix) {
		t.Errorf("unretired features and their indicators must survive: %v", filtered)
	}
}

// dropRetired must never return an empty key set. Training on a stale vector beats
// training on no features at all.
func TestDropRetiredNeverEmptiesTheVector(t *testing.T) {
	keys := []string{"a", "b", "c"}
	all := map[string]bool{"a": true, "b": true, "c": true}
	kept, dropped := dropRetired(keys, all)
	if len(kept) != 3 {
		t.Errorf("retiring everything must be refused, got kept=%v", kept)
	}
	if len(dropped) != 0 {
		t.Errorf("nothing should be reported dropped when the refusal fires, got %v", dropped)
	}

	kept, dropped = dropRetired(keys, map[string]bool{"b": true})
	if len(kept) != 2 || containsStr(kept, "b") {
		t.Errorf("partial retirement should drop only b, got %v", kept)
	}
	if len(dropped) != 1 || dropped[0] != "b" {
		t.Errorf("the dropped name must be reported, got %v", dropped)
	}

	// No retire set at all is a no-op, not an empty result.
	kept, _ = dropRetired(keys, nil)
	if len(kept) != 3 {
		t.Errorf("an empty retire set must change nothing, got %v", kept)
	}
}

// The enforcement read fails OPEN: an unreadable or absent report retires nothing.
// A gate whose failure mode is "train on everything" is recoverable; one whose
// failure mode is "train on nothing" is an outage.
func TestRetiredFeatureKeysFailsOpen(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	if got := retiredFeatureKeys(ctx, st, md.H1d); len(got) != 0 {
		t.Errorf("no stored report must retire nothing, got %v", got)
	}

	// Corrupt payload — still nothing retired, and no panic.
	if err := st.SetMeta(ctx, FeatureHealthMetaPrefix+string(md.H1d), "{not json"); err != nil {
		t.Fatalf("setmeta: %v", err)
	}
	if got := retiredFeatureKeys(ctx, st, md.H1d); len(got) != 0 {
		t.Errorf("an unreadable report must retire nothing, got %v", got)
	}
}

// A stored report is honored, and FeatureHealthFor round-trips it.
func TestRetiredFeatureKeysHonorsAStoredReport(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	blob, err := json.Marshal(map[string]any{
		"horizon": "1d",
		"keep":    []string{"alpha"},
		"retire":  []string{"beta", "gamma"},
		"scores":  []featurehealth.Score{{Name: "beta", Verdict: featurehealth.VerdictRetired}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := st.SetMeta(ctx, FeatureHealthMetaPrefix+"1d", string(blob)); err != nil {
		t.Fatalf("setmeta: %v", err)
	}

	got := retiredFeatureKeys(ctx, st, md.H1d)
	if !got["beta"] || !got["gamma"] {
		t.Errorf("stored retirements must be honored, got %v", got)
	}
	if got["alpha"] {
		t.Error("a kept feature must not be retired")
	}

	rep, ok, err := FeatureHealthFor(ctx, st, md.H1d)
	if err != nil || !ok {
		t.Fatalf("FeatureHealthFor: ok=%v err=%v", ok, err)
	}
	if len(rep.Retire) != 2 || rep.Keep[0] != "alpha" {
		t.Errorf("report did not round-trip: %+v", rep)
	}
}

// The grader is a no-op on an empty feature store rather than an error, and says
// so — an empty store is a young platform, not a fault.
func TestFeatureHealthGraderEmptyStore(t *testing.T) {
	st := openStore(t)
	w := &FeatureHealthGrader{St: st}
	msg, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "nothing to grade") {
		t.Errorf("want an honest empty-state message, got %q", msg)
	}
	// And nothing was published, so the enforcement read still retires nothing.
	if got := retiredFeatureKeys(context.Background(), st, md.H1d); len(got) != 0 {
		t.Errorf("an empty grading run must not retire anything, got %v", got)
	}
}

func containsStr(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
