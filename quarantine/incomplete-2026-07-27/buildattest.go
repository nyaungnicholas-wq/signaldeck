// Build attestation for the research path (2026-07-27).
//
// WHY THIS EXISTS
// ---------------
// The chain already CONVICTED a binary and the binary kept working. Prereg
// record seq 17 carries failed=['matched-null-baseline','deployed-code-revision']
// with the evidence string naming revision 82ef494 as lacking
// researchx.PreregHoldoutEra — and that same convicted process then took a
// research look, charged the multiplicity counter for it, and appended further
// records to the pre-registration chain.
//
// Detection without enforcement is worse than no detection. A look that cannot
// be ledgered still spends a look: the Bonferroni divisor is charged, the
// judgments (the KILLS especially) are discarded, and the surviving trace is a
// narration nothing can be held against. That is precisely the
// keep-the-winners-forget-the-kills asymmetry the judgment ledger exists to
// prevent, in its worst form — both halves missing.
//
// So the research path attests its own build BEFORE it looks, and refuses when
// the attestation fails. This is strictly restrictive: it can only stop the
// system searching and stop it appending to the chain. It cannot make any rule
// easier to clear, cannot raise any published number, and cannot manufacture a
// survivor. The refusal itself is recorded (a research_loop_runs row naming the
// missing mechanism, plus a dq_event) so a refused day is evidence rather than
// silence — and the day gate is deliberately left unset, so a corrected build
// deployed the same day can still take that day's look honestly.
//
// WHAT IS ATTESTED
// ----------------
// The two mechanisms tools/deployment_drift.py checks for on the research side,
// asked of THIS process rather than of a source tree a reviewer has open:
//
//	(1) the blind holdout era is compiled in AND HONOURED. Not "the constant is
//	    non-empty" — that is a tautology in any build that compiles. The probe
//	    is differential: run the real discovery grid twice over the same
//	    synthetic corpus, once with HoldoutEra empty and once with
//	    PreregHoldoutEra, and require the graded week counts to DIFFER. A build
//	    whose search path ignores the holdout produces identical grades and
//	    fails here, which is the drift seq 17 recorded.
//	(2) the judgment ledger can accept what a search would produce, via the
//	    existing store probe. A search whose verdicts cannot be written must
//	    cost no look.
//	(3) the gates THIS binary compiles are at least as strict as the ones the
//	    pre-registration chain froze, and it searches the same atom vocabulary.
//	    A protocol written to a hash chain and never compared against the
//	    running binary is a document, not a control: chain seq 19 freezes the
//	    grid's survival bars and the vocabulary the multiplicity divisor is
//	    derived from, and nothing at execution time asked whether the code
//	    about to search enforces them. The comparison reuses
//	    prereg.DiscoveryProtocol.weakenings, so the direction semantics are the
//	    registrar's own: a STRICTER compiled value passes, only a loosening
//	    refuses, and the vocabulary digest is first-registered-wins (any
//	    difference refuses, because a changed search space changes what a
//	    corrected p-value means).
//
// (1) and (2) are pure computation over ~200 synthetic observations; (3) is one
// read of prereg_records. Nothing here writes, and no branch can make a rule
// easier to clear.
package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// AttestBuildCanLedger reports whether this binary may conduct and record a
// research look. A non-nil error names the missing mechanism and is the exact
// string the refusal row and the dq_event carry.
func AttestBuildCanLedger(ctx context.Context, st *store.Store) error {
	if err := attestHoldoutHonoured(); err != nil {
		return err
	}
	if st == nil {
		return fmt.Errorf("no store: the judgment ledger cannot be probed, so a " +
			"search could not be recorded")
	}
	if err := st.LoopLedgerReady(ctx); err != nil {
		return fmt.Errorf("research_loop_judgments write path unavailable: %w", err)
	}
	return attestGatesMatchChain(ctx, st)
}

// attestGatesMatchChain compares the discovery gates COMPILED INTO THIS BINARY
// against the strictest discovery-protocol record on the pre-registration
// chain, and refuses when the compiled side is looser in any dimension or
// searches a different atom vocabulary.
//
// discoveryProtocol() reads every value out of the package that enforces it, so
// what is compared is what will actually run — not a re-typed copy. The
// strictest fold is used rather than the newest record for the reason
// prereg.ProtocolChainWeakenings gives: "newest wins" is exactly the rule that
// would let a later append walk a survival bar back.
//
// An EMPTY discovery-protocol chain is not a refusal. That is the genesis case
// checkDiscoveryAppendable already treats as unconstrained: nothing was frozen,
// so there is nothing to have drifted from, and refusing would only mean a
// fresh database can never take the look that lets the registrar write the
// record in the first place.
func attestGatesMatchChain(ctx context.Context, st *store.Store) error {
	_, chained, err := st.PreregDiscoveryProtocols(ctx)
	if err != nil {
		return fmt.Errorf("pre-registration chain unreadable, so the gates this build "+
			"compiles cannot be checked against the ones that were frozen: %w", err)
	}
	if len(chained) == 0 {
		return nil
	}
	strict, _ := prereg.StrictestDiscoveryProtocol(chained)
	live := discoveryProtocol()
	// An UNREGISTERED dimension is not a waived one. WeakensRelativeTo reads a
	// zero on the chained side as "constrains nothing" — right for the append
	// guard, wrong here, because it lets this build set any gate the chain
	// never froze to any value and still attest clean. nullDraws is exactly
	// that case, and it is the sampling precision of the null every survival
	// bar is a quantile of. Fail closed: refuse, so the registrar has to append
	// the dimension before the search may run.
	if gaps := strict.CoverageGaps(live); len(gaps) > 0 {
		return fmt.Errorf("this build enforces discovery gates the pre-registration "+
			"chain never froze: %s — an unregistered dimension is one this process is "+
			"free to set after seeing a result, which is the thing registering it prevents",
			strings.Join(gaps, "; "))
	}
	if drifted := live.WeakensRelativeTo(strict); len(drifted) > 0 {
		return fmt.Errorf("this build's compiled discovery gates are LOOSER than the "+
			"pre-registered ones: %s — a protocol on the chain that the running code "+
			"does not enforce is a document, not a control",
			strings.Join(drifted, "; "))
	}
	return nil
}

// attestHoldoutHonoured runs the real grid twice and requires the blind era to
// change what was graded.
func attestHoldoutHonoured() error {
	if researchx.PreregHoldoutEra == "" {
		return fmt.Errorf("researchx.PreregHoldoutEra is empty in this build: the " +
			"pre-registered blind era is not compiled in")
	}
	obs := attestCorpus()
	base := map[string]int{}
	for _, c := range researchx.Discover(obs, researchx.DiscoverConfig{Intent: researchx.IntentReplay}) {
		base[c.ID] = c.Grade.Weeks
	}
	if len(base) == 0 {
		return fmt.Errorf("build attestation could not grade its own probe corpus: " +
			"researchx.Discover judged no rule at all")
	}
	held := map[string]int{}
	for _, c := range researchx.Discover(obs, researchx.DiscoverConfig{
		Intent:     researchx.IntentReplay,
		HoldoutEra: researchx.PreregHoldoutEra,
	}) {
		held[c.ID] = c.Grade.Weeks
	}
	for id, w := range base {
		if hw, ok := held[id]; ok && hw != w {
			return nil
		}
	}
	return fmt.Errorf("researchx.PreregHoldoutEra (%q) changed nothing about what "+
		"this build graded: the discovery path does not withhold the blind era, so "+
		"any confirmation it reports was measured on data the search was fitted to",
		researchx.PreregHoldoutEra)
}

// attestCorpus is a deterministic synthetic corpus spanning the holdout era and
// three others. It carries no signal and is never stored; its only job is to
// make the two Discover passes disagree in a build that honours the holdout.
func attestCorpus() []researchx.Obs {
	eras := []string{"pre-covid", "covid", "post-covid", researchx.PreregHoldoutEra}
	const symbols, weeks = 60, 80
	out := make([]researchx.Obs, 0, symbols*weeks)
	for w := 0; w < weeks; w++ {
		era := eras[w%len(eras)]
		for s := 0; s < symbols; s++ {
			i := w*symbols + s
			out = append(out, researchx.Obs{
				SymbolID: int64(s + 1),
				Week:     int64(w + 1),
				Ts:       int64(w+1) * 604800,
				Vec: map[string]float64{
					"pressure_abs": float64(i%17) / 17,
					"ext_score":    float64(i%13) / 13,
					"rsi_pct":      float64(i%11) / 11,
					"vol_pct":      float64(i%7) / 7,
					"vol_anomaly":  float64(i%5) / 5,
					"price_accel":  float64(i%19) / 19,
					"consec_dir":   float64(i%3) - 1,
					// The call functions read pressure_score; a zero here means
					// "no trade" and the probe would grade nothing at all.
					"pressure_score": float64(i%2)*2 - 1,
				},
				Up:     i%2 == 0,
				FwdRet: float64(i%2)*0.02 - 0.01,
				Era:    era,
			})
		}
	}
	return out
}
