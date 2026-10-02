package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── STAGE 3: append-only, hash-chained prediction ledger (read routes) ──────
//
// WHAT THESE ENDPOINTS PROVE, precisely (this wording was wrong until finding
// C2 and is now the whole point of the file):
//
//   - The hash chain proves INTERNAL CONSISTENCY. Any edit, deletion,
//     reordering or insertion among the stored rows is caught by recomputation,
//     at the exact seq where it happened.
//   - The chain alone does NOT prove ANTERIORITY — that a committed prediction
//     existed before its outcome did. Its only anchor used to be a row in the
//     same SQLite file, and an operator who drops every row, drops that anchor
//     and regenerates a fabricated chain through the same append path gets
//     intact=true from both the cached verify and the full genesis walk. That
//     is reproduced, not conceded in the abstract, by
//     store.TestLedger_ChainProvesConsistencyNotAnteriority.
//   - Anteriority comes from the SIGNED ANCHORS (/api/ledger/anchors): Ed25519
//     signatures over the chain head made with a key held outside the database.
//     Everything at or before the newest anchor whose head the chain still
//     reproduces cannot be regenerated without the key. Everything after that
//     anchor — and all history before the FIRST anchor — carries no anteriority
//     proof at all.
//   - An operator who HOLDS the signing key can regenerate history and re-sign
//     it. The only defence against that is publication: each anchor carries a
//     short digest line the operator posts somewhere that timestamps it
//     independently. Nothing here publishes anything.
//
// Both routes are read-only market-data reads (public under
// SIGNALDECK_PUBLIC_READS), with one deliberate exception documented at
// maybeAnchor: they may APPEND a signed anchor on a cadence.

// anchorEnvDisable turns off cadence anchoring on the read path entirely (for
// an operator who wants anchors written only by an external caller).
const anchorEnvDisable = "SIGNALDECK_LEDGER_ANCHOR_DISABLE"

// anchorWriteBudget caps the OPTIONAL anchor write inside a public read.
//
// Anchoring is best-effort: maybeAnchor already reports wrote:false with a
// reason and the verification is returned regardless. But it took the REQUEST'S
// context, so a write that queued behind the daemon's single writer connection
// (one writer, 103 workers) consumed the whole 30s verify deadline and the
// handler 503'd -- taking the chain result with it.
//
// Measured 2026-09-19 at 531,173 ledger rows: GET /api/ledger/verify timed out
// at 30s, while the same request with SIGNALDECK_LEDGER_ANCHOR_DISABLE=1
// returned intact in 0.73s. The SQLite work is not the cost -- a full chain
// walk reads in 1.61s and the anchor prefix pass in 0.36s. The receipts page,
// whose entire argument is "check my claims yourself", could not verify its own
// chain for any visitor.
//
// So the write gets its own small budget. When the writer is busy the anchor is
// skipped with a reason and the read still answers; the next verify anchors it.
const anchorWriteBudget = 3 * time.Second

// anchorEnvInterval overrides the minimum gap between anchors (Go duration).
const anchorEnvInterval = "SIGNALDECK_LEDGER_ANCHOR_INTERVAL"

// maxAnchorsPerRequest bounds ?limit= on the anchors route. At the default
// cadence 500 anchors is roughly a year of history, and the newest reproducing
// anchor already carries the whole claim — an unbounded limit would only let a
// caller size the response for us.
const maxAnchorsPerRequest = 500

// maxLedgerPerRequest bounds ?limit= on the raw ledger read. 1000 is ten times
// the default and above anything the UI asks for -- web/src/lib/api.ts's
// ledger() defaults to 100 and in fact has no call site at all -- while a
// caller who genuinely wants the whole chain checked has /api/ledger/verify,
// which walks it server-side under a concurrency cap and a deadline instead of
// materialising every row into one response.
const maxLedgerPerRequest = 1000

// maxLedgerRangePerRequest bounds one page of the public /api/ledger/range. At
// ~11k entries a day, 5000 rows is under half a day of chain (~1.6 MB of JSON);
// a verifier pages through the history once, and a larger page would only let
// an anonymous caller size a response for us.
const maxLedgerRangePerRequest = 5000

// ledgerVerifyTimeout bounds one verification request end-to-end. A full
// genesis walk of the live chain measures ~4s at 245k rows; 30s is generous
// headroom under load while making a hung request impossible (finding A11:
// the endpoint used to hold the connection open >180s).
const ledgerVerifyTimeout = 30 * time.Second

// ledgerVerifyConcurrency caps chain verifications running at once across the
// daemon. Verification is CPU-bound (sha256 per row); unbounded concurrent
// walks were the resource-exhaustion lever in finding A11.
const ledgerVerifyConcurrency = 2

// ledgerVerifySem admits at most ledgerVerifyConcurrency verifications; the
// rest 429 immediately rather than queue.
var ledgerVerifySem = make(chan struct{}, ledgerVerifyConcurrency)

// sharedLedgerVerifyCache fronts the DEFAULT (incremental) /api/ledger/verify
// result (step 4, 2026-10-01). Every /proof visit ran the verification itself:
// a COUNT over the ~620k-row prefix plus the suffix walk, then a range COUNT per
// anchor — 2.3-18s measured, and 503 at the 30s deadline three times running
// under load. The result is the same for every caller, so it is built once per
// TTL (by the warmer, normally).
//
// A rebuild runs exactly what the handler ran, maybeAnchor included, so anchors
// are now also written by warmer-driven rebuilds: on the same AnchorDue cadence
// check (one per MinInterval at most) and the same signing. ?full=1 stays live.
//
// It is a PROOF cache (review #6): the result is a claim, so how long a copy may
// stand in is bounded. A rebuild that ran and failed evicts it (the next reader
// gets the real error, as production did before this cache), a copy older than
// 10 minutes is never served, and it is NOT persisted: after a boot /proof waits for a
// verification this process ran, never a previous process's or a previous
// database's. What a served copy claims is stated under ledgerVerifyKey.
var sharedLedgerVerifyCache = func() *swrCache {
	c := newSWRCache(2 * time.Minute)
	c.maxStale = 10 * time.Minute
	return c
}()

// ledgerVerifyBuildTimeout bounds one cached default verification (review #9).
// The request path's 30s ledgerVerifyTimeout is too tight for a build that runs
// for every visitor at once and queues its checkpoint write behind the single
// writer (17s waits measured), but without a bound of its own a wedged build
// held a ledgerVerifySem slot and a cold-build slot for the 10-minute detached
// ceiling. Expiry releases both; the failed rebuild then evicts and the next
// reader retries. A var only so the test can shorten it.
var ledgerVerifyBuildTimeout = 2 * time.Minute

// errLedgerVerifyBusy: ledgerVerifySem was full (finding A11). Answered 429.
var errLedgerVerifyBusy = errors.New("a ledger verification is already running — retry shortly")

// ledgerVerifyKeyTimeout bounds the two lookups ledgerVerifyKey makes. A var
// only so the tests can starve the read pool without waiting 2s a read.
var ledgerVerifyKeyTimeout = 2 * time.Second

// errLedgerVerifyKey: the verify key lookup failed for a reason other than time
// (a scan error, a corrupt row, a missing table). That is a fault, not a busy
// pool: answered 500, counted by the warmer, logged by warnLedgerVerifyKey.
var errLedgerVerifyKey = errors.New("the ledger verify key could not be read")

// ledgerKeyTimeoutStreak is how many key reads in a row may time out before
// that stops reading as load (round 5, F2). One timeout is a busy pool; a pool
// starved for good, or an anchor row too slow to read, would otherwise keep
// /proof on the last build and then on warming indefinitely while the warmer
// logged INFO skips. From the 5th in a row a WARN names the timeout (once a
// minute) and the warmer counts its step as failed, until a key read succeeds.
const ledgerKeyTimeoutStreak = 5

// ledgerKeyTimeouts counts the default verify's key reads that timed out since
// the last one that succeeded.
var ledgerKeyTimeouts atomic.Int64

// ledgerKeyWarnedAt is when warnLedgerVerifyKey last logged (unix nanos).
var ledgerKeyWarnedAt atomic.Int64

// warnLedgerVerifyKey logs a key fault or a timeout streak at WARN at most once
// a minute: either fails every read the same way until it clears, so per-read
// logging would only bury it.
func warnLedgerVerifyKey(msg string, args ...any) {
	now, last := time.Now().UnixNano(), ledgerKeyWarnedAt.Load()
	if now-last >= int64(time.Minute) && ledgerKeyWarnedAt.CompareAndSwap(last, now) {
		slog.Warn(msg+" (logged at most once a minute)", args...)
	}
}

// ledgerVerifyNow stamps computedAt. A var only so a test can see WHEN the stamp
// is taken relative to the row reads.
var ledgerVerifyNow = time.Now

// ledgerVerifyKey keys the cached default verification on the store and the
// NEWEST ANCHOR: its signature, and the entry hash the chain stores today at the
// seq that anchor signed.
//
//   - The signature covers everything the anchor states (when, which seq, how
//     many rows, which head) under the anchor's key. A new anchor is a new key,
//     and so is one deleted and re-signed over a regenerated chain at the same
//     row seq (review G); a row seq would add nothing to it.
//   - The stored hash at the anchored seq changes when the anchored history is
//     rewritten under an unchanged anchor, so that is a new key too and the next
//     read verifies fresh (TestLedgerVerify_FailingOlderAnchorDominatesANewerGoodOne).
//   - The ledger HEAD is deliberately NOT in it. The chain appends ~2.75 times an
//     hour, in bursts (measured on the live database, step-4 review round 2),
//     and with the head in the key nearly every /proof read was a cold verify.
//     Anchors change at most once per MinInterval (6h).
//
// So a cached answer is not "the chain as it is now", and the payload does not
// say it is: it is the verification as of computedAt at head seq headSeq (both
// in the payload, beside the head hash; computedAt is stamped before the rows
// are read). While the key can be read AND something reads it at least once a
// TTL (the warmer does, every minute), a change after that (an append, a
// tamper, a deletion) is reflected by the next rebuild, at most one TTL (2 min)
// plus one rebuild later, and a new or re-signed anchor changes the key and
// forces a fresh verify.
//
// maxStale (10 min) is counted from builtAt, the END of the build that made the
// copy, not from its computedAt: a served copy can be up to maxStale plus that
// build's time (at most ledgerVerifyBuildTimeout) past its computedAt, which it
// carries. A copy that old is served to the first read after a gap with no
// reads (stale-while-revalidate: served, then refreshed behind it), and on
// three paths that serve it on rather than replace it: a key read that times
// out (the last build is served, see cachedLedgerVerify), a refresh that cannot
// run (no cold build slot, or the verify semaphore full), and a refresh whose
// build read another key (the chain moved mid-build). The last two keep the
// copy with its original builtAt, so none of them extends it past maxStale.
//
// Two indexed single-row lookups under one bound. Any error is returned: a
// LedgerEntryHash failure never becomes the no-row key (round-3 V5). No row at
// the anchored seq is a key of its own: the anchored history is gone, which the
// build reports as tamper evidence.
func (d Deps) ledgerVerifyKey(ctx context.Context) (string, error) {
	kctx, cancel := context.WithTimeout(ctx, ledgerVerifyKeyTimeout)
	defer cancel()
	a, ok, err := d.St.LatestLedgerAnchor(kctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return ledgerVerifyKeyOf(d.St, "", ""), nil
	}
	stored, _, err := d.St.LedgerEntryHash(kctx, a.Record.LedgerSeq)
	if err != nil {
		return "", err
	}
	return ledgerVerifyKeyOf(d.St, a.Record.Sig, stored), nil
}

// ledgerVerifyKeyOf spells the key; sig "" is "no anchor yet", stored "" is "no
// row at the anchored seq".
func ledgerVerifyKeyOf(st *store.Store, sig, stored string) string {
	return st.CacheKey() + "|ledger-verify|a" + sig + "|s" + stored
}

// ledgerVerifyKeySeen is the key of the state a build's own anchor check read:
// the newest anchor it checked and the hash it found stored at that anchor's seq.
func ledgerVerifyKeySeen(st *store.Store, av store.LedgerAnchorVerification) string {
	if len(av.Anchors) == 0 {
		return ledgerVerifyKeyOf(st, "", "")
	}
	return ledgerVerifyKeyOf(st, av.Anchors[0].Record.Sig, av.Anchors[0].StoredHead)
}

// lastLedgerVerifyKey holds the key (a string) of the newest default
// verification this process built and filed.
var lastLedgerVerifyKey atomic.Value

// cachedLedgerVerify is the default verification through the shared cache.
//
// A result is filed only under the key of the state its own anchor check read
// (ledgerVerifyKeySeen). The key is read BEFORE the build, so the chain can move
// in between: a fabricated chain is in place for the key read, the honest rows
// are put back for the build, then the fabrication returns, and an honest
// verdict filed under the fabricated key would answer for it (review round 3,
// PGe). When the build read another key, its result answers this build's own
// callers (true as of its computedAt) and is filed under the key it did read,
// never under the requested one. That also files an anchoring build under its
// new anchor (review #10), and only when its check saw that anchor.
//
// When the key read times out (a starved read pool: the 2s bound ran out) it
// starts NO verification. One would need the pool that just failed: round 2
// verified uncached here, and with the pool held a reader waited the full 30s
// for a 503 while the cache held a good answer. Nor does it refresh anything: a
// rebuild filed under a key nobody could read may answer for an anchor it never
// checked. A reader gets the last BUILT answer under the cache's rules (never
// past maxStale; a failed rebuild has already evicted it), or warming at once.
// The warmer is told warming, so its step logs as skipped, until
// ledgerKeyTimeoutStreak reads in a row have timed out: from then on a WARN
// names the timeout and the warmer's step fails, until a key read succeeds.
// Any OTHER key error is a fault, not load: errLedgerVerifyKey, 500, counted by
// the warmer.
func (d Deps) cachedLedgerVerify(ctx context.Context) (map[string]any, error) {
	key, err := d.ledgerVerifyKey(ctx)
	if err == nil {
		ledgerKeyTimeouts.Store(0)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		if ctx.Err() == nil { // the key's own bound ran out, not the caller's
			if n := ledgerKeyTimeouts.Add(1); n >= ledgerKeyTimeoutStreak {
				warnLedgerVerifyKey("ledger verify key reads keep timing out: /api/ledger/verify serves the last build (never past maxStale), then warming, until one reads",
					"consecutive", n, "bound", ledgerVerifyKeyTimeout, "err", err)
				if isWarmer(ctx) {
					return nil, fmt.Errorf("%d ledger verify key reads in a row timed out: %w", n, err)
				}
			}
		}
		if k, _ := lastLedgerVerifyKey.Load().(string); !isWarmer(ctx) && strings.HasPrefix(k, d.St.CacheKey()+"|") {
			if p, ok := sharedLedgerVerifyCache.peek(k); ok {
				return p, nil
			}
		}
		return nil, errWarming
	}
	if err != nil {
		if ctx.Err() == nil { // a caller that left is not a fault
			warnLedgerVerifyKey("ledger verify key unreadable: /api/ledger/verify answers 500 until it reads", "err", err)
		}
		return nil, fmt.Errorf("%w: %w", errLedgerVerifyKey, err)
	}
	return sharedLedgerVerifyCache.get(ctx, key, func(ctx context.Context) (map[string]any, error) {
		out, seen, err := d.buildLedgerVerify(ctx)
		if err != nil {
			return nil, err
		}
		if seen != key {
			sharedLedgerVerifyCache.put(seen, out)
			lastLedgerVerifyKey.Store(seen)
			return nil, uncachedResult{out}
		}
		lastLedgerVerifyKey.Store(key)
		return out, nil
	})
}

// buildLedgerVerify is the default (incremental) verification the handler used
// to run per request: the same semaphore, the same verification, the same
// maybeAnchor, the same anchor check, under ledgerVerifyBuildTimeout. It also
// returns the key of the state its anchor check read.
func (d Deps) buildLedgerVerify(ctx context.Context) (map[string]any, string, error) {
	select {
	case ledgerVerifySem <- struct{}{}:
		defer func() { <-ledgerVerifySem }()
	default:
		return nil, "", errLedgerVerifyBusy
	}
	ctx, cancel := context.WithTimeout(ctx, ledgerVerifyBuildTimeout)
	defer cancel()
	at := ledgerVerifyNow() // before the reads, so every row there at computedAt was read
	v, fullWalk, err := d.St.VerifyLedgerCached(ctx)
	if err != nil {
		return nil, "", err
	}
	av, anchoring, err := d.anchorChecked(ctx, v, false)
	if err != nil {
		return nil, "", err
	}
	return ledgerVerifyPayload(v, fullWalk, av, anchoring, at), ledgerVerifyKeySeen(d.St, av), nil
}

// anchorChecked checks every anchor BEFORE anchoring (802e011). A newer anchor
// over a fabricated chain reproduces fine; the honest OLDER anchor is the thing
// that reports the history is gone, so every anchor is read, not just the
// newest. A trusted (pinned, validly signed) anchor that no longer reproduces
// vetoes signing a new one over a contradicted chain. Unpinned anchors still
// read as failing in tamperEvidence but do not veto: anyone with DB write
// access could add one, and a regeneration still breaks the honest pinned
// anchor. Both paths that sign go through here: the cached default build
// (recompute=false) and ?full=1 (recompute=true).
func (d Deps) anchorChecked(ctx context.Context, v store.LedgerVerification, recompute bool) (store.LedgerAnchorVerification, map[string]any, error) {
	av, err := d.St.VerifyLedgerAnchors(ctx, 0, recompute, ledgeranchor.TrustedKeys())
	if err != nil {
		return av, nil, err
	}
	contradicted, oldest := 0, int64(0)
	for _, a := range av.Anchors {
		if !a.OK && a.SignatureOK && a.PinnedKey {
			if contradicted == 0 || a.Record.LedgerSeq < oldest {
				oldest = a.Record.LedgerSeq
			}
			contradicted++
		}
	}
	if contradicted > 0 {
		return av, map[string]any{"wrote": false, "reason": fmt.Sprintf(
			"%d trusted signed anchor(s) no longer reproduce (oldest at ledger seq %d) — refusing to sign a new anchor over a contradicted chain",
			contradicted, oldest)}, nil
	}
	anchoring := d.maybeAnchor(ctx, v)
	if wrote, _ := anchoring["wrote"].(bool); wrote {
		// Count the anchor just written. On failure keep the earlier answer:
		// the write succeeded and must not read as a 503. TrustedKeys is read
		// AGAIN: the first anchor creates the signing key, so a set taken before
		// it lacks the key and the anchor just written would read as failing.
		if av2, err2 := d.St.VerifyLedgerAnchors(ctx, 0, recompute, ledgeranchor.TrustedKeys()); err2 == nil {
			av = av2
		}
	}
	return av, anchoring, nil
}

// anchorPolicy resolves the cadence from the environment.
func anchorPolicy() store.AnchorPolicy {
	p := store.DefaultAnchorPolicy()
	if v := strings.TrimSpace(os.Getenv(anchorEnvInterval)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			p.MinInterval = d
		}
	}
	return p
}

// anchorKeyErrClass maps a key-loading failure to a path-free description. The
// operator gets the full error from the daemon's own logs; the public payload
// gets the class only.
func anchorKeyErrClass(err error) string {
	switch {
	case errors.Is(err, ledgeranchor.ErrNoKeyPath):
		return "no signing key path is configured (" + ledgeranchor.EnvKeyPath + ")"
	case errors.Is(err, ledgeranchor.ErrKeyPermissions):
		return "the signing key file is not owner-only (0600) and was refused"
	default:
		return "the signing key could not be loaded"
	}
}

// maybeAnchor writes a signed anchor when the cadence is due, and always
// returns a machine-readable account of what happened.
//
// WHY A READ PATH WRITES: an anchor is only worth anything if it is taken
// REPEATEDLY while the history is still honest, and this change owns no
// scheduler (cmd/signaldeckd is not ours to edit). Traffic-driven anchoring
// bounds the cost precisely: at most one Ed25519 signature and one INSERT per
// MinInterval (6h by default), gated first by AnchorDue, which needs no key —
// so a request that was never going to anchor never touches key material. Set
// SIGNALDECK_LEDGER_ANCHOR_DISABLE=1 to turn it off.
//
// It anchors the verification the caller already computed. If that came from
// the incremental path and the incremental path missed a pre-checkpoint payload
// mutation, the anchor commits to a head the recompute-mode anchor check will
// later report as headMatches=false — the failure surfaces as tamper rather
// than hiding, which is the correct direction to fail in.
func (d Deps) maybeAnchor(ctx context.Context, v store.LedgerVerification) map[string]any {
	out := map[string]any{"wrote": false}
	if strings.TrimSpace(os.Getenv(anchorEnvDisable)) == "1" {
		out["reason"] = "disabled by " + anchorEnvDisable
		return out
	}
	p := anchorPolicy()
	out["minInterval"] = p.MinInterval.String()
	now := time.Now()
	// Bounded, and derived from the caller's context: the request's on ?full=1,
	// the cache build's (detached, with its ceiling) on the default path.
	actx, acancel := context.WithTimeout(ctx, anchorWriteBudget)
	defer acancel()
	if _, due, reason, err := d.St.AnchorDue(actx, v, p, now); err != nil {
		out["reason"] = "anchor-due check failed: " + err.Error()
		return out
	} else if !due {
		out["reason"] = reason
		return out
	}
	sg, err := ledgeranchor.LoadOrCreateSigner(ledgeranchor.DefaultKeyPath())
	if err != nil {
		// Fail visibly but say nothing about WHERE the key lives: this payload
		// is a public read, and the underlying errors embed the absolute path
		// (which carries the operator's home directory). A reader needs to know
		// no anteriority evidence is being produced, not the filesystem layout.
		out["reason"] = "signing key unavailable: " + anchorKeyErrClass(err)
		return out
	}
	rec, wrote, reason, err := d.St.MaybeAnchorLedger(actx, sg, v, p, now)
	if err != nil {
		// A busy writer is NOT a verify failure. Say so and let the read answer:
		// the alternative is the 503 this budget exists to end.
		if errors.Is(err, context.DeadlineExceeded) {
			out["reason"] = "writer busy: anchor skipped after " + anchorWriteBudget.String() + "; the next verify will anchor"
			return out
		}
		out["reason"] = "anchor write failed: " + err.Error()
		return out
	}
	if !wrote {
		out["reason"] = reason
		return out
	}
	out["wrote"] = true
	out["record"] = rec
	out["publish"] = rec.PublishLine()
	return out
}

// tamperEvidence renders what the ledger currently proves, in fields a reader
// can test rather than prose they have to trust. provenAnteriorThroughSeq is
// null — never 0 — when no anchor reproduces: 0 would read as "proven from the
// beginning of time", which is the opposite of the truth.
// externalWitness reports whether an anchor digest has been matched against a
// commitment held by somebody other than this machine.
//
// Nothing here can currently say yes, and saying so is the point. ledger_anchors
// stores signature, digest and head hash — no witness reference, no receipt, no
// third-party identifier. The only publication signal in the database is
// meta.anchor_last_published, a unix timestamp this machine wrote about its own
// publish script; tools/anchor_liveness.py reads it to measure a GAP and says in
// its own docstring that local anchors carry no third-party guarantee. A
// timestamp an operator's script wrote is a claim to check, not a receipt: an
// operator who can rewrite the ledger can write that integer too.
//
// So this returns "not established" with the reason, and the caller must not
// promote a locally reproducing anchor into anteriority against the key holder.
// Wire a real receipt through here — a digest matched against a commitment this
// daemon did not author — and `verified` may become true.
func externalWitness() map[string]any {
	return map[string]any{
		"verified": false,
		"receipt":  nil,
		"reason": "no external receipt is verified by this endpoint. ledger_anchors stores " +
			"no witness reference, and meta.anchor_last_published is a timestamp this " +
			"machine wrote about itself — a claim to check, not a receipt. Compare a " +
			"digest from GET /api/ledger/anchors against the third-party copy yourself.",
	}
}

func tamperEvidence(av store.LedgerAnchorVerification, anchoring map[string]any) map[string]any {
	// FOUR DIFFERENT THINGS, AND THIS USED TO REPORT THEM AS ONE (audit F09).
	//
	//   1. stored-head comparison   anchors checked against the head hashes the
	//                               database already holds
	//   2. payload recomputation    the chain re-derived from payloads (?full=1)
	//   3. valid local signature     an Ed25519 anchor signed earlier still
	//                               reproduces over the current chain
	//   4. external witness         that digest also sits somewhere this machine
	//                               does not control
	//
	// `proven` is (3), and (3) alone. It was being published as
	// detectsOperatorRegeneration=true — a field that names the operator — in the
	// same payload whose own prose says "an operator holding the signing key can
	// re-sign a fabricated chain; only the externally published anchor digest
	// defeats that". Both sentences cannot be right. The prose is the right one:
	// an operator with the key deletes the history, re-appends a fabrication, and
	// signs a fresh anchor over it, and every local check passes.
	//
	// So (3) is reported as what it is, and the operator-resistant claim is gated
	// on (4), which nothing can currently satisfy.
	proven := av.ProvenThroughSeq != nil && av.FailingAnchors == 0
	witness := externalWitness()
	witnessed, _ := witness["verified"].(bool)
	claim := "Edits are detectable: any modified, deleted, reordered or inserted row breaks the recomputation at that seq. " +
		"Wholesale regeneration by the operator is NOT detectable from the chain alone — deleting every row and re-appending a fabricated chain verifies intact."
	if proven {
		claim += " Against an adversary WITHOUT the Ed25519 signing key it IS detectable at or before the newest reproducing anchor: regenerating that history and still producing its signature requires the key, which is held outside the database. " +
			"Entries appended after that anchor, and any history predating the first anchor, carry no anteriority proof even against that adversary. " +
			"Against the OPERATOR, who does hold the key, nothing here is evidence: he can re-sign a fabricated chain and every check on this page passes. " +
			"Only an anchor digest matched against a commitment held by a third party defeats that, and this endpoint verifies no such receipt — see externalWitness."
	} else if av.ProvenThroughSeq != nil {
		claim += " A newer anchor still reproduces, but an older signed anchor does not, so NOTHING in this ledger has anteriority evidence — only edit-detection."
	} else {
		claim += " No anchor currently reproduces, so NOTHING in this ledger has anteriority evidence — only edit-detection."
	}
	// provenAnterior* read as proven whenever non-null (api.ts: null means
	// "nothing is proven"), so they carry the same gate as `proven`.
	var provenSeq, provenCount, provenTs *int64
	if proven {
		provenSeq, provenCount, provenTs = av.ProvenThroughSeq, av.ProvenThroughCount, av.ProvenThroughTs
	}
	if av.Mode == "stored" {
		claim += " This summary compared anchors against the STORED head hashes; ?full=1 re-derives the chain from payloads, which is the check an auditor should run."
	}
	if av.FailingAnchors > 0 {
		claim = "TAMPER EVIDENCE: " + strconv.Itoa(av.FailingAnchors) + " previously-signed anchor(s) NO LONGER REPRODUCE " +
			"against the chain in this database. A signed anchor that stops reproducing is not a stale record — it is " +
			"positive evidence that history was rewritten after it was signed. Anteriority is NOT claimed here whatever " +
			"newer anchors say, because an anchor written over an already-fabricated chain reproduces perfectly. " +
			"Inspect GET /api/ledger/anchors and treat every number on this page as unproven until it is explained. " +
			claim
	}
	return map[string]any{
		"failingAnchors":  av.FailingAnchors,
		"firstFailingSeq": av.FirstFailingSeq,
		"detectsEdits":    true,

		// (4) gates the operator claim. False until a receipt is verified.
		"detectsOperatorRegeneration": proven && witnessed,
		"externalWitness":             witness,

		// (3): a locally reproducing signature, named as such. The
		// provenAnterior* numbers below are this same extent, and they are
		// anteriority only against an adversary who does NOT hold the key —
		// `anteriorityScope` says so in the payload rather than leaving it to
		// prose a client can drop.
		"localAnchorsReproduce":           proven,
		"localAnchorReproducesThroughSeq": av.ProvenThroughSeq,
		"provenAnteriorThroughSeq":        provenSeq,
		"provenAnteriorThroughCount":      provenCount,
		"provenAnteriorAsOf":              provenTs,
		"anteriorityScope": "against an adversary WITHOUT the signing key; the operator holds it, " +
			"so this is not evidence against him",

		// (1) vs (2): which check produced the above.
		"anchorCheckMode":      av.Mode,
		"storedHeadComparison": av.Mode == "stored",
		"payloadRecomputed":    av.Mode == "recomputed",

		"anchorCount": av.AnchorCount,
		"anchoring":   anchoring,
		"claim":       claim,
	}
}

// ledgerVerify reports the chain's integrity via the incremental checkpoint
// path (cold-load precompute wave): only the suffix since the last intact
// checkpoint is re-hashed unless the checkpoint anchor mismatches — the
// tamper signal that forces (and fails) a full walk. Same answer as a full
// recomputation on an untampered chain, ~12s cheaper at 200k rows.
//
// ?full=1 forces the complete genesis walk — the deliberate auditor's path.
// The disclosed limit of the fast path: a payload mutated strictly BEFORE the
// checkpoint that leaves every stored hash untouched is internally
// inconsistent but only caught by the full walk (any rewrite that keeps the
// chain consistent must change the anchor hash and IS caught).
//
// The response carries the anchor summary alongside the chain result, because
// "intact" on its own is exactly the number that misled a reviewer: intact is a
// statement about consistency, and only the anchors speak to anteriority.
func (d Deps) ledgerVerify(w http.ResponseWriter, r *http.Request) {
	full := r.URL.Query().Get("full") == "1"
	// ?full=1 walks the whole chain at least twice (VerifyLedger + the anchor
	// recompute). That is the auditor's path, not an anonymous one: on a public
	// deployment it is a resource-exhaustion lever, so it needs identity.
	if full && !d.isOperator(r) {
		httpErr(w, http.StatusUnauthorized, "?full=1 re-derives the whole chain and requires authentication; the default incremental verify is public")
		return
	}
	// The default answer comes from the shared cache (sharedLedgerVerifyCache);
	// only the auditor's ?full=1 walk below runs per request.
	if !full {
		out, err := d.cachedLedgerVerify(r.Context())
		switch {
		case errors.Is(err, errLedgerVerifyBusy):
			w.Header().Set("Retry-After", "5")
			httpErr(w, http.StatusTooManyRequests, err.Error())
			return
		case errors.Is(err, errWarming):
			writeWarming(w)
			return
		case errors.Is(err, errLedgerVerifyKey):
			// Logged by cachedLedgerVerify, at most once a minute; not per read.
			httpErr(w, http.StatusInternalServerError, "internal error")
			return
		case err != nil:
			// A build that ran out its ledgerVerifyBuildTimeout is a 503 to
			// retry, as on ?full=1, not the opaque 500.
			ledgerVerifyErr(w, r.Context(), err)
			return
		}
		writeJSON(w, out)
		return
	}
	// At most ledgerVerifyConcurrency verifications run at once, daemon-wide.
	// Anything beyond that gets an immediate 429 instead of stacking CPU-bound
	// chain walks behind each other until the daemon starves (finding A11).
	select {
	case ledgerVerifySem <- struct{}{}:
		defer func() { <-ledgerVerifySem }()
	default:
		w.Header().Set("Retry-After", "5")
		httpErr(w, http.StatusTooManyRequests, errLedgerVerifyBusy.Error())
		return
	}
	// Hard deadline: even a full genesis walk finishes in seconds (measured
	// ~4s at 245k rows); anything still running past this bound is contention,
	// and holding the connection open indefinitely (the observed >180s hang)
	// helps nobody.
	ctx, cancel := context.WithTimeout(r.Context(), ledgerVerifyTimeout)
	defer cancel()

	at := ledgerVerifyNow()
	v, err := d.St.VerifyLedger(ctx)
	if err != nil {
		ledgerVerifyErr(w, ctx, err)
		return
	}
	av, anchoring, err := d.anchorChecked(ctx, v, true)
	if err != nil {
		ledgerVerifyErr(w, ctx, err)
		return
	}
	writeJSON(w, ledgerVerifyPayload(v, true, av, anchoring, at))
}

// ledgerVerifyErr answers a failed verification: 503 when it ran out of time
// (?full=1 at ledgerVerifyTimeout, the cached default build at
// ledgerVerifyBuildTimeout), else 500.
func ledgerVerifyErr(w http.ResponseWriter, ctx context.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		httpErr(w, http.StatusServiceUnavailable, "ledger verification ran out of time — retry shortly; the default verify is the incremental path (no ?full=1)")
		return
	}
	httpInternal(w, err)
}

// ledgerVerifyPayload is the verify payload. computedAt (at, taken before the
// rows were read), headSeq and head say when it was computed and which chain
// head it covers, because the default path serves a cached result (the bound is
// under ledgerVerifyKey).
func ledgerVerifyPayload(v store.LedgerVerification, fullWalk bool, av store.LedgerAnchorVerification, anchoring map[string]any, at time.Time) map[string]any {
	out := map[string]any{
		"intact":         v.Intact,
		"count":          v.Count,
		"headSeq":        v.HeadSeq,
		"head":           v.HeadHash,
		"incremental":    !fullWalk,
		"computedAt":     at.UTC().Format(time.RFC3339),
		"tamperEvidence": tamperEvidence(av, anchoring),
		"verifiedNote":   "verified incrementally from the last intact checkpoint (hash chains verify incrementally by design); a checkpoint-anchor mismatch forces a full walk and reads as tamper; ?full=1 forces the complete genesis walk, the only path that catches a stored-hash-preserving payload mutation before the checkpoint",
		"intactMeans":    "the stored rows are internally consistent — NOT that they were written when they claim. Read tamperEvidence for what is actually proven.",
	}
	if v.BrokenAtSeq != nil {
		out["brokenAtSeq"] = *v.BrokenAtSeq
	}
	return out
}

// ledgerAnchors is the auditor's anchor view: every signed commitment, whether
// its signature verifies, whether the chain still reproduces its head, and the
// short digest line to compare against whatever was published externally.
//
// ?full=1 re-derives the chain from genesis payloads (ignoring stored hashes)
// — the strongest available check and the slow one. Without it the anchor is
// compared against the stored head hash and prefix count.
// ?limit= caps how many anchors are checked (newest first, default 50, hard
// ceiling maxAnchorsPerRequest — the parameter is attacker-controlled on a
// public read and must not size a response for us).
func (d Deps) ledgerAnchors(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxAnchorsPerRequest {
		limit = maxAnchorsPerRequest
	}
	recompute := r.URL.Query().Get("full") == "1"
	// Same guards as ledgerVerify: recompute mode re-derives the chain from
	// genesis, so it is gated on identity, bounded in concurrency, and given a
	// hard deadline (finding A11 applies to this route equally).
	if recompute && !d.isOperator(r) {
		httpErr(w, http.StatusUnauthorized, "?full=1 re-derives the whole chain and requires authentication; the stored-hash check is public")
		return
	}
	select {
	case ledgerVerifySem <- struct{}{}:
		defer func() { <-ledgerVerifySem }()
	default:
		w.Header().Set("Retry-After", "5")
		httpErr(w, http.StatusTooManyRequests, "a ledger verification is already running — retry shortly")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ledgerVerifyTimeout)
	defer cancel()
	av, err := d.St.VerifyLedgerAnchors(ctx, limit, recompute, ledgeranchor.TrustedKeys())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			httpErr(w, http.StatusServiceUnavailable, "anchor verification exceeded "+ledgerVerifyTimeout.String()+" — retry without ?full=1")
			return
		}
		httpInternal(w, err)
		return
	}
	out := map[string]any{
		"mode":           av.Mode,
		"anchorCount":    av.AnchorCount,
		"checked":        av.Checked,
		"anchors":        av.Anchors,
		"pubKeys":        av.DistinctPubKeys,
		"tamperEvidence": tamperEvidence(av, map[string]any{"wrote": false, "reason": "anchors are written on the verify path"}),
		"publishNote":    "post the newest anchor's `publish` line somewhere that timestamps it independently. A digest on a third party's record is the only evidence an operator holding the signing key cannot rewrite. Signature verification needs no secret, but a signature proves something only under a TRUSTED key: check each anchor pubKey against internal/ledgeranchor/pinned_pubkeys.txt (or a key published externally), not just against the row it sits in.",
		"keyNote":        "the signing key lives outside the database (path from " + ledgeranchor.EnvKeyPath + ", owner-only 0600, generated on first use). A key stored beside the data it signs would prove nothing. Only anchors signed by the current key or a key pinned in internal/ledgeranchor/pinned_pubkeys.txt count; any other key reads as failing.",
	}
	writeJSON(w, out)
}

// ledger returns the committed ledger entries for one symbol+horizon (newest
// first). ?symbol=&market= identify the symbol; ?horizon= defaults to 1d;
// ?limit= caps the count (default 100).
func (d Deps) ledger(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	// ?limit= is attacker-controlled on a PUBLIC read (/api/ledger is in
	// publicRoutes), so it must not size the response for us -- the same rule
	// ledgerAnchors already states and enforces with maxAnchorsPerRequest.
	// This was the only unbounded limit left in the package; every other route
	// clamps. Measured against the live daemon, ?limit=100000 on one symbol
	// returned its entire 2,301-entry history, and that number grows with the
	// record forever, so the ceiling is what stops it rather than the data
	// happening to be small today.
	limit := 100
	requested := 0
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			requested = n
			limit = n
		}
	}
	if limit > maxLedgerPerRequest {
		limit = maxLedgerPerRequest
	}
	entries, err := d.St.LedgerFor(r.Context(), s.ID, h, limit)
	if err != nil {
		httpInternal(w, err)
		return
	}
	// Say so when the clamp bit. This endpoint exists to be audited, and an
	// auditor who asked for the whole chain and silently received a prefix
	// would compute a head hash that disagrees with the published one and have
	// nothing in the response explaining why.
	out := map[string]any{
		"symbol":  s.Symbol,
		"horizon": h,
		"count":   len(entries),
		"limit":   limit,
		"entries": entries,
	}
	if requested > limit {
		out["truncated"] = true
		out["requestedLimit"] = requested
	}
	writeJSON(w, out)
}

// ledgerRange serves the chain by sequence number, ascending: ?from= (default
// 1) and ?limit= (default 1000, at most maxLedgerRangePerRequest). It exists so
// an outsider can recompute every link between two heads published in the
// public anchors repo without trusting this server's own verify answer —
// /api/ledger's per-symbol slices cannot be linked to a head. `next` is the seq
// to request next, or null once a page came back short (the head was reached).
func (d Deps) ledgerRange(w http.ResponseWriter, r *http.Request) {
	from := int64(1)
	if q := r.URL.Query().Get("from"); q != "" {
		n, err := strconv.ParseInt(q, 10, 64)
		if err != nil || n < 1 {
			httpErr(w, http.StatusBadRequest, "from must be a positive sequence number")
			return
		}
		from = n
	}
	limit := 1000
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 {
			httpErr(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}
	if limit > maxLedgerRangePerRequest {
		limit = maxLedgerRangePerRequest
	}
	entries, err := d.St.LedgerRange(r.Context(), from, limit)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if entries == nil {
		entries = []store.LedgerEntry{}
	}
	var next any
	if len(entries) == limit {
		next = entries[len(entries)-1].Seq + 1
	}
	writeJSON(w, map[string]any{
		"from": from, "limit": limit, "count": len(entries), "entries": entries, "next": next,
	})
}

// registerLedger wires the Stage-3 prediction-ledger read routes.
func (d Deps) registerLedger(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ledger/verify", d.ledgerVerify)
	mux.HandleFunc("GET /api/ledger/anchors", d.ledgerAnchors)
	mux.HandleFunc("GET /api/ledger/range", d.ledgerRange)
	mux.HandleFunc("GET /api/ledger", d.ledger)
}
