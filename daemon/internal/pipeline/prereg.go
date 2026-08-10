// prereg-registrar — writes the frozen predictor claims into the chain, once,
// before their forecasts start resolving.
//
// Timing is the whole point: the value of a pre-registration collapses to zero
// the moment it could have been written after seeing results. So this runs at
// daemon start, appends only the kinds not already present, and never rewrites
// an existing row. If it has not run before the first grade lands, the record
// it writes is worth strictly less — and the payload says which side of that
// line it fell on rather than leaving the reader to work it out from dates.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// PreregRegistrar appends missing pre-registration records.
type PreregRegistrar struct {
	St  *store.Store
	Now func() time.Time
	// GraderPath overrides where the grading script is read from when
	// digesting it for the grading-protocol record (tests, unusual layouts).
	GraderPath string
	// DocPath overrides where the human-readable protocol document is read
	// from when digesting it for the prereg-document record.
	DocPath string
	// RegistryPath overrides where the graded registry artifact is read from
	// when charging a grading LOOK (tests, unusual layouts).
	RegistryPath string
	// GraderVersion overrides how the grader's committed version is resolved.
	// Production leaves it nil and git answers; tests inject so the assertion
	// is about the registrar's behaviour, not the operator's working tree.
	// It returns the commit and whether the file has uncommitted changes.
	GraderVersion func() (commit string, dirty bool, err error)
	// GraderAtCommit returns the SHA-256 of the grader file AS IT EXISTS at the
	// given commit. Production leaves it nil and `git show <commit>:<path>`
	// answers; tests inject so the assertion is about the registrar refusing an
	// unresolvable pin, not about a fixture repository's history.
	GraderAtCommit func(commit string) (string, error)
}

// docKind is the chain kind for PREREGISTRATION.md — the human-readable
// statement of the 2026-08-14 structural grading protocol (claim freeze, null
// choice, min-n/min-days gates, no post-hoc slicing). The specs and grading
// protocol freeze the machine-readable rules; this record freezes the PROSE a
// reader is pointed at, so the explanation cannot be quietly rewritten to fit
// the outcomes either. SpecHash is the file's raw SHA-256, checkable with
// nothing but `shasum -a 256 PREREGISTRATION.md`.
const docKind = "prereg-document"

// docRel is the document's repo-relative path, resolved like the grader path.
const docRel = "PREREGISTRATION.md"

func (w *PreregRegistrar) Name() string { return "prereg-registrar" }

// Interval is long because this is a one-time write that self-heals: after the
// first pass every kind is present and each run is a single SELECT.
func (w *PreregRegistrar) Interval() time.Duration { return 12 * time.Hour }

func (w *PreregRegistrar) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *PreregRegistrar) Run(ctx context.Context) (string, error) {
	have, err := w.St.PreregKinds(ctx)
	if err != nil {
		return "", err
	}
	// Newest stored spec hash per kind. A kind already registered whose spec
	// hash has CHANGED is not a no-op: the claim in code no longer matches the
	// claim on the chain, and skipping it would let the two diverge silently —
	// which is the exact failure pre-registration exists to prevent. It gets an
	// AMENDMENT record instead, so the chain carries both the original claim and
	// the correction, in order, and neither can be mistaken for the other.
	latest, err := w.St.LatestPreregHashes(ctx)
	if err != nil {
		return "", err
	}
	now := w.now().Unix()
	written, amended := 0, 0
	// The filings-drift HYPOTHESIS (the research loop's second orthogonal
	// non-price signal — see researchloop.go) rides the same chain and the
	// same amendment discipline as the claim specs. The loop refuses to freeze
	// a single filingsdrift21 forecast until this record exists, so
	// registration-before-first-grade holds by construction.
	for _, spec := range append(prereg.Specs(), filingsDriftSpec()) {
		if have[spec.Kind] {
			if prior, ok := latest[spec.Kind]; ok && prior != spec.Hash() {
				blob, err := json.Marshal(spec)
				if err != nil {
					return "", fmt.Errorf("marshal amendment %s: %w", spec.Kind, err)
				}
				if _, err := w.St.AppendPrereg(ctx, prereg.Record{
					Ts: now, Kind: spec.Kind,
					SpecJSON: string(blob), SpecHash: spec.Hash(),
					Note: "AMENDMENT — the claim in code changed after registration. The prior record " +
						"stands unaltered above this one; read them in order. An amendment is evidence of a " +
						"correction, not a replacement of the original commitment.",
				}); err != nil {
					return "", fmt.Errorf("append amendment %s: %w", spec.Kind, err)
				}
				amended++
			}
			continue
		}
		blob, err := json.Marshal(spec)
		if err != nil {
			return "", fmt.Errorf("marshal %s: %w", spec.Kind, err)
		}
		if _, err := w.St.AppendPrereg(ctx, prereg.Record{
			Ts: now, Kind: spec.Kind,
			SpecJSON: string(blob), SpecHash: spec.Hash(),
			Note: "initial registration — written before any forecast of this kind resolved",
		}); err != nil {
			return "", fmt.Errorf("append %s: %w", spec.Kind, err)
		}
		written++
	}
	// The grading protocol rides the same chain under its own kind. A claim
	// frozen against a grader that can still move is only half a commitment —
	// registering the grader's version and decision rules closes the other
	// half. The grader file is digested NOW, so an edit to it after
	// registration changes the protocol hash and appends an AMENDMENT on the
	// next pass, with the same discipline the claim specs already follow.
	skipped := ""
	// Kinds this pass deliberately did not attempt (an unreadable grader or
	// document). They are excluded from the postcondition below so a genuine,
	// already-reported skip is not re-reported as a silent failure.
	notAttempted := map[string]bool{}
	digest, derr := w.graderDigest()
	// A digest taken from a DIRTY working tree names a version that exists
	// nowhere but this machine's disk: no commit contains it, so nobody —
	// including the operator — can reproduce the registration. Refusing is
	// strictly restrictive (fewer records get written, and only ones a
	// verifier can resolve), so the refusal is recorded as a DQ event rather
	// than written onto the chain.
	commit, dirty, cerr := w.graderVersion(digest)
	if cerr == nil && dirty {
		cerr = fmt.Errorf("grader has uncommitted changes; a working-tree digest is not "+
			"reproducible from any commit (%s)", prereg.GraderRel)
	}
	switch {
	case derr != nil:
		notAttempted[prereg.ProtocolKind] = true
		// The claims above are time-critical and already written; a missing
		// grader file must not roll them back. Say so and self-heal next run.
		skipped = fmt.Sprintf("; grading protocol NOT registered (grader unreadable: %v)", derr)
	case cerr != nil:
		notAttempted[prereg.ProtocolKind] = true
		skipped = fmt.Sprintf("; grading protocol NOT registered (%v)", cerr)
		if err := w.St.InsertDQ(ctx, md.DQEvent{
			Ts:   now,
			Kind: "error",
			Detail: fmt.Sprintf("prereg-registrar refused to register the grading protocol: %v — "+
				"the chain may only pin a grader version a verifier can fetch by commit", cerr),
		}); err != nil {
			return "", fmt.Errorf("record grading-protocol refusal: %w", err)
		}
	default:
		proto := prereg.GradingProtocol(digest, commit)
		blob, err := json.Marshal(proto)
		if err != nil {
			return "", fmt.Errorf("marshal grading protocol: %w", err)
		}
		if have[prereg.ProtocolKind] {
			if prior, ok := latest[prereg.ProtocolKind]; ok && prior != proto.Hash() {
				if _, err := w.St.AppendPrereg(ctx, prereg.Record{
					Ts: now, Kind: prereg.ProtocolKind,
					SpecJSON: string(blob), SpecHash: proto.Hash(),
					Note: "AMENDMENT — the grading protocol or the grader file changed after registration. " +
						"The prior protocol stands unaltered above this one; read them in order.",
				}); err != nil {
					return "", fmt.Errorf("append protocol amendment: %w", err)
				}
				amended++
			}
		} else {
			if _, err := w.St.AppendPrereg(ctx, prereg.Record{
				Ts: now, Kind: prereg.ProtocolKind,
				SpecJSON: string(blob), SpecHash: proto.Hash(),
				Note: "initial registration — the grading protocol (grader version + digest, refusal " +
					"rules, verdict map) frozen before any forecast it will grade resolved",
			}); err != nil {
				return "", fmt.Errorf("append grading protocol: %w", err)
			}
			written++
		}
	}
	// The protocol DOCUMENT rides the chain too. Its digest here is what lets
	// an outsider check that the prose protocol — claim freeze, null choice,
	// min-n/min-days gates, no post-hoc slicing — predates the 2026-08-14
	// grading, not just the numbers. Same discipline as the grader: an edited
	// document re-digests, and the change appends an AMENDMENT on the next pass.
	if digest, derr := w.fileDigest(docRel, w.DocPath); derr != nil {
		notAttempted[docKind] = true
		skipped += fmt.Sprintf("; protocol document NOT registered (%s unreadable: %v)", docRel, derr)
	} else {
		blob, err := json.Marshal(struct {
			Path         string `json:"path"`
			SHA256       string `json:"sha256"`
			FirstGrading string `json:"firstGrading"`
		}{Path: docRel, SHA256: digest, FirstGrading: "2026-08-14"})
		if err != nil {
			return "", fmt.Errorf("marshal protocol document: %w", err)
		}
		if have[docKind] {
			if prior, ok := latest[docKind]; ok && prior != digest {
				if _, err := w.St.AppendPrereg(ctx, prereg.Record{
					Ts: now, Kind: docKind,
					SpecJSON: string(blob), SpecHash: digest,
					Note: "AMENDMENT — PREREGISTRATION.md changed after registration. The prior " +
						"record stands unaltered above this one; read them in order.",
				}); err != nil {
					return "", fmt.Errorf("append document amendment: %w", err)
				}
				amended++
			}
		} else {
			if _, err := w.St.AppendPrereg(ctx, prereg.Record{
				Ts: now, Kind: docKind,
				SpecJSON: string(blob), SpecHash: digest,
				Note: "initial registration — PREREGISTRATION.md (the 2026-08-14 structural grading " +
					"protocol: claim freeze, null choice, min-n/min-days gates, no post-hoc slicing) " +
					"frozen before the first grading. specHash is the file's SHA-256.",
			}); err != nil {
				return "", fmt.Errorf("append protocol document: %w", err)
			}
			written++
		}
	}
	// The directional AUTO-RETIRE rule rides the chain under its own kind,
	// registered while every directional verdict is still INSUFFICIENT. The
	// registry grader (tools/accuracy_registry.py) enforces the criterion and
	// writes the retire flag; ModelHealthWorker consumes it; this record — and
	// the chain head ops/anchor-publish.sh pushes to the public anchors repo —
	// is what makes the threshold provably older than the data it will judge.
	rule := prereg.AutoRetireRule()
	ruleBlob, err := json.Marshal(rule)
	if err != nil {
		return "", fmt.Errorf("marshal auto-retire rule: %w", err)
	}
	if have[prereg.RetireRuleKind] {
		if prior, ok := latest[prereg.RetireRuleKind]; ok && prior != rule.Hash() {
			if _, err := w.St.AppendPrereg(ctx, prereg.Record{
				Ts: now, Kind: prereg.RetireRuleKind,
				SpecJSON: string(ruleBlob), SpecHash: rule.Hash(),
				Note: "AMENDMENT — the auto-retire rule changed after registration. The prior rule " +
					"stands unaltered above this one; read them in order. A kill criterion revised " +
					"after the evidence started arriving is exactly what this chain exists to expose.",
			}); err != nil {
				return "", fmt.Errorf("append auto-retire amendment: %w", err)
			}
			amended++
		}
	} else {
		if _, err := w.St.AppendPrereg(ctx, prereg.Record{
			Ts: now, Kind: prereg.RetireRuleKind,
			SpecJSON: string(ruleBlob), SpecHash: rule.Hash(),
			Note: "initial registration — the directional FAILED-forward auto-retire rule " +
				"(at the frozen evidence floors, an effective-N Wilson 95% upper bound below the " +
				"prequential null grades FAILED, publishes retire=true, and the daemon stops " +
				"publishing the horizon) committed while every directional verdict was still " +
				"INSUFFICIENT — before the evidence it will judge could exist.",
		}); err != nil {
			return "", fmt.Errorf("append auto-retire rule: %w", err)
		}
		written++
	}
	// The unmatched-null QUARANTINE manifest rides the chain under its own kind,
	// but only once the outcome worker has actually frozen a set. Registering it
	// is what converts "we decided to exempt some rows" into an externally fixed,
	// countable commitment: the digest here pins the exact membership, so an
	// extension of the exempt set re-digests, appends a visible AMENDMENT, and
	// fails the daemon's per-tick verification. Absent a manifest there is
	// nothing exempt and nothing to register.
	if qm, ok, err := w.St.NullQuarantineManifest(ctx); err != nil {
		return "", fmt.Errorf("read null-quarantine manifest: %w", err)
	} else if ok {
		q := prereg.NullQuarantineRecord(qm.Digest, qm.NRows, store.NullAmendmentEpoch)
		qBlob, err := json.Marshal(q)
		if err != nil {
			return "", fmt.Errorf("marshal null-quarantine manifest: %w", err)
		}
		if have[prereg.NullQuarantineKind] {
			if prior, ok := latest[prereg.NullQuarantineKind]; ok && prior != q.Hash() {
				if _, err := w.St.AppendPrereg(ctx, prereg.Record{
					Ts: now, Kind: prereg.NullQuarantineKind,
					SpecJSON: string(qBlob), SpecHash: q.Hash(),
					Note: "AMENDMENT — the unmatched-null quarantine manifest changed after " +
						"registration. The set was registered as NOT growable, so a changed digest " +
						"means the exempt set was extended or edited. The prior record stands " +
						"unaltered above this one; read them in order.",
				}); err != nil {
					return "", fmt.Errorf("append null-quarantine amendment: %w", err)
				}
				amended++
			}
		} else {
			if _, err := w.St.AppendPrereg(ctx, prereg.Record{
				Ts: now, Kind: prereg.NullQuarantineKind,
				SpecJSON: string(qBlob), SpecHash: q.Hash(),
				Note: "initial registration — the frozen, non-extendable set of historical outcome " +
					"rows exempted from the unmatched-null startup invariant. They stay ungraded " +
					"(NO BASELINE) and out of every denominator; the exemption raises no number.",
			}); err != nil {
				return "", fmt.Errorf("append null-quarantine manifest: %w", err)
			}
			written++
		}
	}
	// THE LOOK COUNTER. Every grade of the registry is a look at the same
	// accruing rows, and the published interval is widened by the number of
	// looks taken (prereg.MultiplicityRule). The count therefore has to live
	// somewhere append-only that a log rotation cannot rewind, which is this
	// chain. Charged per GRADE, not per registrar wakeup: the graded_at stamp
	// of the registry artifact is the de-duplication key, so a pass that sees
	// nothing new appends nothing.
	//
	// Failure here is reported, never fatal, and never rolls back the claims
	// above. It is also strictly one-directional: an unreadable registry means
	// no look is charged this pass and the divisor stays where it was, which
	// is the conservative direction only in the sense that it cannot INVENT
	// looks; the counter it maintains is monotone and never decreases.
	looks, lerr := w.chargeLook(ctx, now)
	if lerr != nil {
		skipped += fmt.Sprintf("; grading look NOT charged (%v)", lerr)
	} else if looks > 0 {
		written++
	}

	// POSTCONDITION. Every branch above is conditional, and a pre-registration
	// that silently fails to register turns a running hypothesis test into a
	// permanent no-op with no error surface — the loop's filings-drift gate, for
	// one, reads this chain and quietly freezes nothing forever if its kind is
	// absent. So the intended chain is CHECKED rather than assumed: re-read the
	// kinds actually on the chain and name any that should be there and are not.
	intended := []string{prereg.RetireRuleKind}
	for _, spec := range append(prereg.Specs(), filingsDriftSpec()) {
		intended = append(intended, spec.Kind)
	}
	intended = append(intended, prereg.ProtocolKind, docKind)
	after, err := w.St.PreregKinds(ctx)
	if err != nil {
		return "", fmt.Errorf("verify prereg chain: %w", err)
	}
	var missing []string
	for _, k := range intended {
		if !after[k] && !notAttempted[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("pre-registration chain incomplete after the write pass — "+
			"missing kind(s) %s; anything gated on these registrations is a silent "+
			"no-op until they exist", strings.Join(missing, ", "))
	}
	if written == 0 && amended == 0 {
		return "all predictor claims already pre-registered" + skipped, nil
	}
	if written == 0 {
		return fmt.Sprintf("appended %d AMENDMENT record(s) — a registered claim changed in code%s", amended, skipped), nil
	}
	return fmt.Sprintf("pre-registered %d record(s) (first gradable %s), %d amendment(s)%s",
		written, prereg.FirstGradableOn, amended, skipped), nil
}

// filingsDriftSpec freezes the HYPOTHESIS behind the research loop's
// filings-event candidate (researchloop.go) — the second orthogonal non-price
// signal after the sentiment-correlation study. Unlike the structregime specs
// this is not an advertised accuracy being defended; it is a question being
// asked. So the single band carries the coin-flip null as its "claim", which
// makes the registry's HOLDING verdict mean only "not worse than chance", and
// the actual PASS rule is stated in the resolution text before any outcome
// exists: supported only when the day-clustered interval's LOWER bound clears
// 0.50.
func filingsDriftSpec() prereg.Spec {
	return prereg.Spec{
		Kind:        FilingsDriftKind,
		Question:    "After a 10-Q/10-K lands, does the sign of the first post-filing session's return persist over the next 21 trading sessions (post-filing drift)?",
		HorizonDays: 21,
		Resolution: "Reaction bar = first daily bar whose session opened at or after filed_ts (an intra-session " +
			"filing rolls to the next session — no lookahead by construction). Call = sign of the reaction " +
			"return close[t]/close[t-1]-1; a zero reaction is not called. Resolved once 21 newer daily bars " +
			"exist: correct when sign(close[t+21]/close[t]-1) matches the call; a zero forward return is not " +
			"graded. Routed through regime_outcomes and graded by tools/accuracy_registry.py exactly like every " +
			"other kind — day-clustered Wilson interval, min-n and min-days refusals. The hypothesis is " +
			"SUPPORTED only if the interval's lower bound clears 0.50.",
		Bands: []prereg.Band{{MinConviction: 0, Claimed: 0.5}},
		Baseline: "A fair coin on the drift sign (0.50). This is a hypothesis under test, not an advertised " +
			"edge: 0.50 is registered as the claim precisely so no verdict can be read as validation of an " +
			"accuracy table that was never measured.",
		KnownWeakness: "Event timing comes from the filings table but direction still reads the tape (the " +
			"reaction-bar return), so this is event-conditioned price behaviour, not fundamentals parsing. " +
			"Freeze lag is bounded at 7 calendar days after filing, so up to ~5 of the 21 forward sessions may " +
			"already have elapsed at freeze time; selection is unconditional (every fresh 10-Q/10-K in the " +
			"tracked universe is frozen), which is what keeps that lag from cherry-picking. The universe is " +
			"currently-tracked symbols, so drift through delisting is unobserved (survivorship).",
	}
}

// graderDigest hashes the grading script the registered protocol points at.
func (w *PreregRegistrar) graderDigest() (string, error) {
	return w.fileDigest(prereg.GraderRel, w.GraderPath)
}

// graderVersion resolves the grader's committed version and worktree state,
// and — the part that makes the pin mean anything — proves that the commit it
// returns actually CONTAINS the digest about to be registered.
//
// The two halves of the pin used to be independent measurements: the commit
// came from `git log -1 -- <grader>` and the digest from the working-tree file.
// Nothing forced them to describe the same bytes, and on the live chain they
// did not: seq 11 pins commit 04395a2e (whose grader hashes dafc5213) against
// graderSha256 25cd8923, while today's file hashes edb36dce — three different
// versions in one record. PREREGISTRATION.md §2 tells a verifier to fetch the
// grader at the pinned commit and check its SHA-256; against a record like that
// the documented procedure cannot be completed at all, so the guarantee is
// unenforceable in both directions.
//
// So the digest is re-derived from the commit itself (`git show <commit>:<path>`)
// and required to match. A mismatch takes the same refuse-and-DQ path as the
// dirty-tree check rather than writing an unverifiable record. Strictly
// restrictive: it can only prevent a registration, never create one.
func (w *PreregRegistrar) graderVersion(digest string) (string, bool, error) {
	var commit string
	var dirty bool
	var err error
	if w.GraderVersion != nil {
		commit, dirty, err = w.GraderVersion()
	} else {
		if commit, err = w.graderCommit(); err == nil {
			dirty, err = w.graderDirty()
		}
	}
	if err != nil {
		return "", false, err
	}
	if dirty {
		// A dirty tree is already refused by the caller, and `git show` on the
		// commit would compare against bytes nobody is registering.
		return commit, dirty, nil
	}
	if digest == "" {
		return commit, dirty, fmt.Errorf("no grader digest to verify against commit %s", commit)
	}
	committed, err := w.graderDigestAtCommit(commit)
	if err != nil {
		return commit, dirty, fmt.Errorf("read grader at commit %s: %w", commit, err)
	}
	if committed != digest {
		return commit, dirty, fmt.Errorf("grader digest %s does not exist at the pinned commit %s "+
			"(the grader at that commit hashes %s) — a record pinning a commit that does not "+
			"contain its own digest cannot be verified by the procedure PREREGISTRATION.md §2 "+
			"documents, in either direction", digest, commit, committed)
	}
	return commit, dirty, nil
}

// graderDigestAtCommit hashes the grader file as committed, not as it sits on
// disk. `git show` is the only reader that cannot be fooled by a working tree.
func (w *PreregRegistrar) graderDigestAtCommit(commit string) (string, error) {
	if w.GraderAtCommit != nil {
		return w.GraderAtCommit(commit)
	}
	out, err := w.git("show", commit+":"+prereg.GraderRel)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(out))
	return hex.EncodeToString(sum[:]), nil
}

// graderCommit is the last commit that touched the grader, read at
// registration time. A hard-coded constant silently drifts off the file it
// claims to name; git is the only source that cannot.
func (w *PreregRegistrar) graderCommit() (string, error) {
	out, err := w.git("log", "-1", "--format=%H", "--", prereg.GraderRel)
	if err != nil {
		return "", fmt.Errorf("read grader commit: %w", err)
	}
	sha := strings.TrimSpace(out)
	if len(sha) != 40 {
		return "", fmt.Errorf("grader commit %q is not a full commit hash — the grader is not "+
			"tracked in this repository, so the chain cannot pin a fetchable version", sha)
	}
	return sha, nil
}

// graderDirty reports whether the grader has uncommitted changes.
func (w *PreregRegistrar) graderDirty() (bool, error) {
	out, err := w.git("status", "--porcelain", "--", prereg.GraderRel)
	if err != nil {
		return false, fmt.Errorf("check grader worktree: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

// git runs a git command at the repo root, located the same way fileDigest
// locates repo-relative files.
func (w *PreregRegistrar) git(args ...string) (string, error) {
	root, err := w.repoRoot()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	return string(out), err
}

// repoRoot resolves the directory the grader path is relative to.
func (w *PreregRegistrar) repoRoot() (string, error) {
	if w.GraderPath != "" {
		abs, err := filepath.Abs(w.GraderPath)
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(filepath.Dir(abs), filepath.Dir(prereg.GraderRel)), nil
	}
	for _, c := range []string{"..", ".", filepath.Join("..", "..", "..")} {
		if _, err := os.Stat(filepath.Join(c, prereg.GraderRel)); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("cannot locate repo root containing %s", prereg.GraderRel)
}

// fileDigest hashes a repo-relative file. The daemon's launchd
// WorkingDirectory is <repo>/daemon and `go test` runs from a package
// directory, so a short list of candidates covers both without
// configuration; override wins for anything unusual.
func (w *PreregRegistrar) fileDigest(rel, override string) (string, error) {
	candidates := []string{
		filepath.Join("..", rel),             // daemon CWD: <repo>/daemon
		rel,                                  // repo-root CWD
		filepath.Join("..", "..", "..", rel), // package dir under daemon/internal/...
	}
	if override != "" {
		candidates = []string{override}
	}
	var lastErr error
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:]), nil
	}
	return "", lastErr
}

// chargeLook appends a grading-look record when the registry artifact carries a
// graded_at this chain has not already charged for. Returns 1 if a look was
// appended, 0 if the grade was already counted.
//
// The counter written is one above the HIGHEST already on the chain, computed
// as max(record count, highest carried counter) — the same max()-fold
// ResearchLoop.priorSearches uses over its meta, worker_runs and durable
// sources, and for the identical reason: a look already paid for must never be
// refundable by a source coming back short.
func (w *PreregRegistrar) chargeLook(ctx context.Context, now int64) (int, error) {
	gradedAt, family, err := w.registryGradedAt()
	if err != nil {
		return 0, err
	}
	if gradedAt == "" {
		return 0, fmt.Errorf("registry %s carries no graded_at stamp", prereg.RegistryRel)
	}
	recs, err := w.St.PreregRecords(ctx)
	if err != nil {
		return 0, err
	}
	highest, count := 0, 0
	for _, r := range recs {
		if r.Kind != prereg.LookKind {
			continue
		}
		count++
		var l prereg.Look
		if err := json.Unmarshal([]byte(r.SpecJSON), &l); err != nil {
			// An unreadable look record still PROVES a look was taken, so it
			// keeps counting toward the total even though its stamp is lost.
			continue
		}
		if l.GradedAt == gradedAt {
			return 0, nil // this grade is already charged
		}
		if l.Counter > highest {
			highest = l.Counter
		}
	}
	if count > highest {
		highest = count
	}
	look := prereg.Look{Counter: highest + 1, GradedAt: gradedAt, Registry: prereg.RegistryRel,
		Family: family}
	blob, err := json.Marshal(look)
	if err != nil {
		return 0, fmt.Errorf("marshal grading look: %w", err)
	}
	if _, err := w.St.AppendPrereg(ctx, prereg.Record{
		Ts: now, Kind: prereg.LookKind,
		SpecJSON: string(blob), SpecHash: look.Hash(),
		Note: "grading look " + strconv.Itoa(look.Counter) + " — a grade was taken over the " +
			"accruing rows at " + gradedAt + ". Charged whether or not it changed a verdict: " +
			"counting only the looks that moved something would make a null grade free, which " +
			"is the optional stopping this counter exists to price.",
	}); err != nil {
		return 0, fmt.Errorf("append grading look: %w", err)
	}
	return 1, nil
}

// registryGradedAt reads the graded_at stamp out of the graded registry
// artifact. A refusal envelope carries the STALE graded_at forward, so a run
// the grader refused is correctly not charged as a look.
func (w *PreregRegistrar) registryGradedAt() (string, int, error) {
	candidates := []string{
		filepath.Join("..", prereg.RegistryRel),
		prereg.RegistryRel,
		filepath.Join("..", "..", "..", prereg.RegistryRel),
	}
	if w.RegistryPath != "" {
		candidates = []string{w.RegistryPath}
	}
	var lastErr error
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		var payload struct {
			GradedAt string `json:"graded_at"`
			// FamilySize is carried onto the look record so the monotone family
			// term of the divisor survives a truncated chain, exactly as the
			// look Counter does.
			FamilySize int `json:"family_size"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return "", 0, fmt.Errorf("parse %s: %w", p, err)
		}
		return payload.GradedAt, payload.FamilySize, nil
	}
	return "", 0, lastErr
}
