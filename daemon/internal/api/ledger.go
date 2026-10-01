package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
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
// TTL (by the warmer, normally) and persisted across restarts.
//
// A rebuild runs exactly what the handler ran, maybeAnchor included, so anchors
// are now also written by warmer-driven rebuilds: on the same AnchorDue cadence
// check (one per MinInterval at most) and the same signing. ?full=1 stays live.
//
// It is a PROOF cache (review #6): the result is a claim, so it never outlives
// its evidence. A failed rebuild evicts it (the next reader gets the real error,
// as production did before this cache), a copy older than 10 minutes is never
// served, and it is NOT persisted: after a boot /proof waits for a verification
// this process ran, never a previous process's or a previous database's.
var sharedLedgerVerifyCache = &swrCache{ttl: 2 * time.Minute, maxStale: 10 * time.Minute, ent: map[string]*swrEntry{}}

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

// lastAnchorSeq remembers each store's newest anchor seq (CacheKey -> int64),
// for when the lookup itself is starved by a busy read pool.
var lastAnchorSeq sync.Map

// ledgerVerifyKey keys the cached verification on the store AND its newest
// anchor. A new anchor — written by a rebuild here, by an operator script, or
// over a regenerated chain — must reach the next reader, not wait out a TTL
// behind a result computed before it existed. It is one indexed row, bounded at
// 2s; past that the last seq seen stands in.
func (d Deps) ledgerVerifyKey(ctx context.Context) string {
	actx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	a, ok, err := d.St.LatestLedgerAnchor(actx)
	if err == nil {
		seq := int64(0)
		if ok {
			seq = a.RowSeq
		}
		lastAnchorSeq.Store(d.St.CacheKey(), seq)
		return d.St.CacheKey() + "|ledger-verify|a" + strconv.FormatInt(seq, 10)
	}
	if v, ok := lastAnchorSeq.Load(d.St.CacheKey()); ok {
		return d.St.CacheKey() + "|ledger-verify|a" + strconv.FormatInt(v.(int64), 10)
	}
	return d.St.CacheKey() + "|ledger-verify|unknown"
}

// cachedLedgerVerify is the default verification through the shared cache.
func (d Deps) cachedLedgerVerify(ctx context.Context) (map[string]any, error) {
	return sharedLedgerVerifyCache.get(ctx, d.ledgerVerifyKey(ctx), d.buildLedgerVerify)
}

// buildLedgerVerify is the default (incremental) verification the handler used
// to run per request: the same semaphore, the same verification, the same
// maybeAnchor, the same anchor check, under ledgerVerifyBuildTimeout.
func (d Deps) buildLedgerVerify(ctx context.Context) (map[string]any, error) {
	select {
	case ledgerVerifySem <- struct{}{}:
		defer func() { <-ledgerVerifySem }()
	default:
		return nil, errLedgerVerifyBusy
	}
	ctx, cancel := context.WithTimeout(ctx, ledgerVerifyBuildTimeout)
	defer cancel()
	v, fullWalk, err := d.St.VerifyLedgerCached(ctx)
	if err != nil {
		return nil, err
	}
	anchoring := d.maybeAnchor(ctx, v)
	av, err := d.St.VerifyLedgerAnchors(ctx, 0, false)
	if err != nil {
		return nil, err
	}
	out := ledgerVerifyPayload(v, fullWalk, av, anchoring)
	// The anchor just written re-keys the cache, and this result already covers
	// it (the anchor check ran after the write): file it under that key too, or
	// the next reader pays a second cold verify (review #10).
	if wrote, _ := anchoring["wrote"].(bool); wrote {
		sharedLedgerVerifyCache.put(d.ledgerVerifyKey(ctx), out)
	}
	return out, nil
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
	} else {
		claim += " No anchor currently reproduces, so NOTHING in this ledger has anteriority evidence — only edit-detection."
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
		"provenAnteriorThroughSeq":        av.ProvenThroughSeq,
		"provenAnteriorThroughCount":      av.ProvenThroughCount,
		"provenAnteriorAsOf":              av.ProvenThroughTs,
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
		if errors.Is(err, errLedgerVerifyBusy) {
			w.Header().Set("Retry-After", "5")
			httpErr(w, http.StatusTooManyRequests, err.Error())
			return
		}
		if err != nil {
			httpCacheErr(w, err)
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

	v, err := d.St.VerifyLedger(ctx)
	if err != nil {
		ledgerVerifyErr(w, ctx, err)
		return
	}
	anchoring := d.maybeAnchor(ctx, v)
	// Every anchor, not just the newest. A newer anchor over a fabricated chain
	// reproduces fine; the honest OLDER anchor is the thing that reports the
	// history is gone, and checking only the newest would never surface it.
	av, err := d.St.VerifyLedgerAnchors(ctx, 0, true)
	if err != nil {
		ledgerVerifyErr(w, ctx, err)
		return
	}
	writeJSON(w, ledgerVerifyPayload(v, true, av, anchoring))
}

// ledgerVerifyErr answers a failed ?full=1 walk: 503 at the deadline, else 500.
func ledgerVerifyErr(w http.ResponseWriter, ctx context.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		httpErr(w, http.StatusServiceUnavailable, "ledger verification exceeded "+ledgerVerifyTimeout.String()+" — retry, or use the incremental path (no ?full=1)")
		return
	}
	httpInternal(w, err)
}

// ledgerVerifyPayload is the verify payload. computedAt says when it was computed,
// because the default path serves a cached result.
func ledgerVerifyPayload(v store.LedgerVerification, fullWalk bool, av store.LedgerAnchorVerification, anchoring map[string]any) map[string]any {
	out := map[string]any{
		"intact":         v.Intact,
		"count":          v.Count,
		"head":           v.HeadHash,
		"incremental":    !fullWalk,
		"computedAt":     time.Now().UTC().Format(time.RFC3339),
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
	av, err := d.St.VerifyLedgerAnchors(ctx, limit, recompute)
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
		"publishNote":    "post the newest anchor's `publish` line somewhere that timestamps it independently. A digest on a third party's record is the only evidence an operator holding the signing key cannot rewrite. Signature verification needs no secret: the public key is in every anchor row.",
		"keyNote":        "the signing key lives outside the database (path from " + ledgeranchor.EnvKeyPath + ", owner-only 0600, generated on first use). A key stored beside the data it signs would prove nothing.",
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
