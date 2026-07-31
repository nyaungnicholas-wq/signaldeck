package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// appendDiscoveryProtocol writes one discovery-protocol record onto the chain.
func appendDiscoveryProtocol(t *testing.T, st *store.Store, d prereg.DiscoveryProtocol, ts int64) {
	t.Helper()
	blob, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := st.AppendPrereg(context.Background(), prereg.Record{
		Ts: ts, Kind: prereg.DiscoveryProtocolKind,
		SpecJSON: string(blob), SpecHash: d.Hash(),
		Note: "test fixture",
	}); err != nil {
		t.Fatalf("append discovery protocol: %v", err)
	}
}

// An EMPTY chain froze nothing, so there is nothing for the binary to have
// drifted from. Refusing here would mean a fresh database can never take the
// look that lets the registrar write the first record.
func TestAttestPassesWhenNoDiscoveryProtocolIsChained(t *testing.T) {
	st := newLoopStore(t)
	if err := attestGatesMatchChain(context.Background(), st); err != nil {
		t.Fatalf("genesis chain must not refuse: %v", err)
	}
}

// The gates this binary compiles ARE the ones the registrar writes, so a chain
// carrying exactly them must attest clean. If this ever fails, the record and
// the code have diverged and the refusal below is the correct behaviour.
func TestAttestPassesWhenChainedProtocolMatchesTheBinary(t *testing.T) {
	st := newLoopStore(t)
	appendDiscoveryProtocol(t, st, discoveryProtocol(), time.Now().Unix())
	if err := attestGatesMatchChain(context.Background(), st); err != nil {
		t.Fatalf("a build enforcing exactly the chained gates must attest: %v", err)
	}
}

// A STRICTER compiled value is not drift. The chain is a floor, not an equality
// constraint: tightening a survival bar after registration can only kill rules.
func TestAttestPassesWhenTheBinaryIsStricterThanTheChain(t *testing.T) {
	st := newLoopStore(t)
	loose := discoveryProtocol()
	loose.MinPositiveEras = 1             // chain froze a LOWER era floor
	loose.MaxAlpha = 0.10                 // and a LARGER family-wise budget
	loose.CatastrophicRegimeMargin = 0.30 // and a WIDER collapse tolerance
	loose.FragilityRetentionFloor = 0.10  // and a LOWER retention floor
	appendDiscoveryProtocol(t, st, loose, time.Now().Unix())
	if err := attestGatesMatchChain(context.Background(), st); err != nil {
		t.Fatalf("a build stricter than the chain must still attest: %v", err)
	}
}

// The case the fix exists for: the chain froze a bar the running code does not
// enforce. Without this comparison the pre-registration is a document.
func TestAttestRefusesWhenTheBinaryIsLooserThanTheChain(t *testing.T) {
	st := newLoopStore(t)
	strict := discoveryProtocol()
	strict.MinPositiveEras += 2                         // chain demands more positive eras
	strict.MinWeeks += 100                              // and far more graded weeks
	strict.MaxAlpha = discoveryProtocol().MaxAlpha / 10 // and a tighter budget
	appendDiscoveryProtocol(t, st, strict, time.Now().Unix())

	err := attestGatesMatchChain(context.Background(), st)
	if err == nil {
		t.Fatal("a build enforcing looser gates than the chain froze must refuse")
	}
	for _, want := range []string{"minPositiveEras", "minWeeks", "maxAlpha"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %s: %v", want, err)
		}
	}
}

// The atom vocabulary is first-registered-wins, not a floor: the multiplicity
// divisor is derived FROM the grid, so ANY change to it changes what a
// corrected p-value means. A different digest refuses in either direction.
func TestAttestRefusesWhenTheAtomVocabularyDrifted(t *testing.T) {
	st := newLoopStore(t)
	d := discoveryProtocol()
	d.AtomVocabularyDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	appendDiscoveryProtocol(t, st, d, time.Now().Unix())

	err := attestGatesMatchChain(context.Background(), st)
	if err == nil {
		t.Fatal("a search space that differs from the registered one must refuse")
	}
	if !strings.Contains(err.Error(), "atomVocabularyDigest") {
		t.Errorf("refusal does not name the vocabulary: %v", err)
	}
}

// The engine is the ONLY worker that writes into the belief ledger, and it used
// to promote unconditionally while the loop — which can only record — refused.
// An unattested build must promote nothing and say so.
func TestEngineRefusesToDiscoverWhenItsBuildCannotBeAttested(t *testing.T) {
	st := newLoopStore(t)
	seedLoopCorpus(t, st, 4000)
	ctx := context.Background()
	w := &ResearchEngineWorker{St: st, Attest: unattested}

	msg, err := w.Run(ctx)
	if err == nil {
		t.Fatalf("an unattested promoting worker must fail loudly, got %q", msg)
	}
	if !strings.Contains(err.Error(), "promoted nothing") {
		t.Errorf("refusal does not say nothing was promoted: %v", err)
	}
	hyps, herr := st.LedgerHypotheses(ctx)
	if herr != nil {
		t.Fatalf("read hypotheses: %v", herr)
	}
	for _, h := range hyps {
		if h.Family == "auto" {
			t.Fatalf("an unattested build promoted %s into the belief ledger", h.ID)
		}
	}
}
