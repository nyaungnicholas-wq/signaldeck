package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newLedgerServer wires the Stage-3 ledger routes behind the real middleware.
//
// The anchor signing key is pinned into a temp dir: the verify route writes an
// anchor on a cadence, and a test run must never create (or reuse) the
// operator's real key at ~/.signaldeck/ledger_anchor.key.
func newLedgerServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	srv, st, d := newTestServer(t, func(c *config.Config) {
		// ?full=1 requires identity (finding A11); tests authenticate with
		// this token via ledgerGet.
		c.APIToken = ledgerTestToken
		if mutate != nil {
			mutate(c)
		}
	})
	// The bearer token maps to the admin user, which a fresh temp DB lacks.
	if _, err := st.CreateUser(context.Background(), "admin", "hash", true); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	mux := http.NewServeMux()
	d.registerLedger(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

// ledgerTestToken authenticates test requests to the auth-gated full walk.
const ledgerTestToken = "ledger-test-token"

// ledgerGet GETs a ledger route with the bearer token attached.
func ledgerGet(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ledgerTestToken)
	res, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// ledgerVerifyBody is the verify payload, including the tamper-evidence block
// that states what the ledger proves and — crucially — what it does not.
type ledgerVerifyBody struct {
	Intact      bool   `json:"intact"`
	Count       int64  `json:"count"`
	Head        string `json:"head"`
	BrokenAtSeq *int64 `json:"brokenAtSeq"`
	Tamper      struct {
		DetectsEdits bool `json:"detectsEdits"`
		// detectsOperatorRegeneration is gated on an EXTERNAL receipt (audit
		// F09); localAnchorsReproduce is the local-signature check the
		// assertions below are really about.
		DetectsOperatorRegeneration bool   `json:"detectsOperatorRegeneration"`
		LocalAnchorsReproduce       bool   `json:"localAnchorsReproduce"`
		AnteriorityScope            string `json:"anteriorityScope"`
		AnchorCheckMode             string `json:"anchorCheckMode"`
		StoredHeadComparison        bool   `json:"storedHeadComparison"`
		PayloadRecomputed           bool   `json:"payloadRecomputed"`
		ExternalWitness             struct {
			Verified bool   `json:"verified"`
			Reason   string `json:"reason"`
		} `json:"externalWitness"`
		ProvenAnteriorThroughSeq   *int64 `json:"provenAnteriorThroughSeq"`
		ProvenAnteriorThroughCount *int64 `json:"provenAnteriorThroughCount"`
		AnchorCount                int64  `json:"anchorCount"`
		FailingAnchors             int    `json:"failingAnchors"`
		FirstFailingSeq            *int64 `json:"firstFailingSeq"`
		Claim                      string `json:"claim"`
		Anchoring                  struct {
			Wrote   bool   `json:"wrote"`
			Reason  string `json:"reason"`
			Publish string `json:"publish"`
		} `json:"anchoring"`
	} `json:"tamperEvidence"`
}

// getLedgerVerify GETs the verify route and decodes the payload.
func getLedgerVerify(t *testing.T, srv *httptest.Server, query string) ledgerVerifyBody {
	t.Helper()
	res := ledgerGet(t, srv, "/api/ledger/verify"+query)
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body ledgerVerifyBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// appendLedgerRows appends n chained entries for one symbol. prob varies the
// payload: re-appending the SAME payloads reproduces the same head, which is
// not a rewrite of history at all — a fabrication has to change what was
// claimed.
func appendLedgerRows(t *testing.T, st *store.Store, symID int64, n int, prob float64) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := st.AppendLedger(ctx, store.LedgerEntry{
			PredictedAt: int64(1000 + i), SymbolID: symID, Horizon: md.H1d, BarTs: int64(i),
			RawProb: prob, CalProb: prob, FeatureHash: "fh", ModelVersion: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLedgerVerify_AnchorsAndStatesWhatIsProven: the verify payload must not
// stop at intact=true. Finding C2 was that "intact" was read as tamper-evidence
// when it only means internal consistency, so the endpoint now anchors on a
// cadence and reports, in testable fields, how far anteriority is actually
// proven — null, never 0, when it is proven nowhere.
func TestLedgerVerify_AnchorsAndStatesWhatIsProven(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 20, 0.5)

	first := getLedgerVerify(t, srv, "")
	if !first.Intact || first.Count != 20 {
		t.Fatalf("intact/count = %v/%d, want true/20", first.Intact, first.Count)
	}
	if !first.Tamper.Anchoring.Wrote {
		t.Fatalf("no anchor written on the first verify: %q", first.Tamper.Anchoring.Reason)
	}
	if !strings.HasPrefix(first.Tamper.Anchoring.Publish, "SIGNALDECK-LEDGER-ANCHOR") {
		t.Errorf("publish line = %q, want the postable digest line", first.Tamper.Anchoring.Publish)
	}
	if !first.Tamper.LocalAnchorsReproduce ||
		first.Tamper.ProvenAnteriorThroughSeq == nil || *first.Tamper.ProvenAnteriorThroughSeq != 20 {
		t.Fatalf("after anchoring: detects=%v provenThroughSeq=%v, want true/20",
			first.Tamper.LocalAnchorsReproduce, first.Tamper.ProvenAnteriorThroughSeq)
	}

	// Cadence: an immediate second call must not anchor again, and must say why.
	second := getLedgerVerify(t, srv, "")
	if second.Tamper.Anchoring.Wrote {
		t.Error("second verify wrote another anchor inside the cadence window")
	}
	if second.Tamper.Anchoring.Reason == "" {
		t.Error("declined anchor with no stated reason")
	}

	// ── The operator regenerates history (the C2 attack) ───────────────────
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM prediction_ledger`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM meta WHERE k='ledger_verify_checkpoint'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name='prediction_ledger'`); err != nil {
		t.Fatal(err)
	}
	// The fabrication: every call restated as a confident winner.
	appendLedgerRows(t, st, sym.ID, 20, 0.93)

	after := getLedgerVerify(t, srv, "?full=1")
	if !after.Intact {
		t.Fatal("the regenerated chain should still report intact — the chain cannot see this, which is why the anchor must")
	}
	if after.Tamper.LocalAnchorsReproduce {
		t.Fatal("payload claims operator regeneration is detected while the anchor no longer reproduces")
	}
	if after.Tamper.ProvenAnteriorThroughSeq != nil {
		t.Errorf("provenAnteriorThroughSeq = %v after regeneration, want null (withheld, never 0)", *after.Tamper.ProvenAnteriorThroughSeq)
	}
	if !strings.Contains(after.Tamper.Claim, "NOT detectable") {
		t.Errorf("claim does not state the limit plainly: %q", after.Tamper.Claim)
	}
	// And it must refuse to paper over the break by anchoring the fabricated
	// chain in the same request.
	if after.Tamper.Anchoring.Wrote {
		t.Error("anchored the regenerated chain — a fresh anchor over fabricated history restores a claim that is false")
	}
}

// TestLedgerAnchorsEndpoint: the auditor route lists every anchor with its
// verification result and the digest line to compare against what was published
// externally — the only check an operator holding the signing key cannot pass
// by rewriting the database.
func TestLedgerAnchorsEndpoint(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 10, 0.5)
	if v := getLedgerVerify(t, srv, ""); !v.Tamper.Anchoring.Wrote {
		t.Fatalf("setup: no anchor written (%s)", v.Tamper.Anchoring.Reason)
	}

	res := ledgerGet(t, srv, "/api/ledger/anchors?full=1")
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Mode        string   `json:"mode"`
		AnchorCount int64    `json:"anchorCount"`
		PubKeys     []string `json:"pubKeys"`
		Anchors     []struct {
			OK          bool   `json:"ok"`
			SignatureOK bool   `json:"signatureOK"`
			HeadMatches bool   `json:"headMatches"`
			Publish     string `json:"publish"`
			Record      struct {
				LedgerSeq   int64  `json:"ledgerSeq"`
				LedgerCount int64  `json:"ledgerCount"`
				HeadHash    string `json:"headHash"`
				PubKey      string `json:"pubKey"`
				Sig         string `json:"sig"`
			} `json:"record"`
		} `json:"anchors"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Mode != "recomputed" {
		t.Errorf("mode = %q, want recomputed under ?full=1", body.Mode)
	}
	if body.AnchorCount != 1 || len(body.Anchors) != 1 {
		t.Fatalf("anchorCount=%d anchors=%d, want 1/1", body.AnchorCount, len(body.Anchors))
	}
	a := body.Anchors[0]
	if !a.OK || !a.SignatureOK || !a.HeadMatches {
		t.Fatalf("anchor on an untouched chain: ok=%v sig=%v head=%v", a.OK, a.SignatureOK, a.HeadMatches)
	}
	if a.Record.LedgerSeq != 10 || a.Record.LedgerCount != 10 {
		t.Errorf("anchor pins seq/count %d/%d, want 10/10", a.Record.LedgerSeq, a.Record.LedgerCount)
	}
	// The served record must be independently verifiable with no secret: the
	// public key travels with it, and the published digest re-derives.
	rec := ledgeranchor.Record{
		CreatedAt: 0, LedgerSeq: a.Record.LedgerSeq, LedgerCount: a.Record.LedgerCount,
		HeadHash: a.Record.HeadHash, Alg: ledgeranchor.Alg, PubKey: a.Record.PubKey, Sig: a.Record.Sig,
	}
	if rec.Verify() {
		t.Error("a record with the wrong createdAt verified — the timestamp is not covered by the signature")
	}
	if len(body.PubKeys) != 1 || body.PubKeys[0] != a.Record.PubKey {
		t.Errorf("pubKeys = %v, want the anchor's key", body.PubKeys)
	}
	if !strings.HasPrefix(a.Publish, "SIGNALDECK-LEDGER-ANCHOR") {
		t.Errorf("publish = %q, want the postable digest line", a.Publish)
	}
}

// TestLedgerVerify_AnchoringCanBeDisabled: an operator who does not want a read
// path to write must be able to turn it off, and the payload must say that is
// why no anchor exists rather than leaving an unexplained gap.
func TestLedgerVerify_AnchoringCanBeDisabled(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1")
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)

	body := getLedgerVerify(t, srv, "")
	if body.Tamper.Anchoring.Wrote {
		t.Fatal("anchored despite " + anchorEnvDisable + "=1")
	}
	if !strings.Contains(body.Tamper.Anchoring.Reason, anchorEnvDisable) {
		t.Errorf("reason = %q, want it to name the disabling switch", body.Tamper.Anchoring.Reason)
	}
	if body.Tamper.AnchorCount != 0 || body.Tamper.LocalAnchorsReproduce ||
		body.Tamper.ProvenAnteriorThroughSeq != nil {
		t.Errorf("with no anchors the payload must claim nothing: %+v", body.Tamper)
	}
	if !strings.Contains(body.Tamper.Claim, "NOTHING in this ledger has anteriority evidence") {
		t.Errorf("claim omits the plain statement that nothing is proven: %q", body.Tamper.Claim)
	}
}

// TestLedgerVerifyEndpoint: after appending a small chain, /api/ledger/verify
// reports intact + the correct count and head; after tampering a row it reports
// intact=false with the broken seq.
func TestLedgerVerifyEndpoint(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	var head string
	for i := 0; i < 5; i++ {
		e, err := st.AppendLedger(ctx, store.LedgerEntry{
			PredictedAt: int64(1000 + i), SymbolID: sym.ID, Horizon: md.H1d, BarTs: int64(i),
			RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		head = e.EntryHash
	}

	res, err := newClient(t).Get(srv.URL + "/api/ledger/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (public read default)", res.StatusCode)
	}
	var body struct {
		Intact      bool   `json:"intact"`
		Count       int64  `json:"count"`
		Head        string `json:"head"`
		BrokenAtSeq *int64 `json:"brokenAtSeq"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Intact || body.Count != 5 || body.Head != head || body.BrokenAtSeq != nil {
		t.Fatalf("intact chain response wrong: %+v (want head %q)", body, head)
	}

	// Tamper with seq 3 (payload mutated, stored hashes untouched), then
	// re-verify with ?full=1 → intact=false at seq 3. The full walk is the
	// deliberate audit path: this tamper preserves every stored hash and the
	// checkpoint anchor, so the incremental default cannot see it — the
	// endpoint discloses exactly that in verifiedNote (cold-load precompute
	// wave; see internal/store/ledgercache.go).
	if _, err := st.DB().ExecContext(ctx, // read pool can exec; a single connection
		`UPDATE prediction_ledger SET raw_prob=raw_prob+1 WHERE seq=3`); err != nil {
		t.Fatal(err)
	}
	res2 := ledgerGet(t, srv, "/api/ledger/verify?full=1")
	defer res2.Body.Close() //nolint:errcheck
	var body2 struct {
		Intact      bool   `json:"intact"`
		BrokenAtSeq *int64 `json:"brokenAtSeq"`
	}
	if err := json.NewDecoder(res2.Body).Decode(&body2); err != nil {
		t.Fatal(err)
	}
	if body2.Intact {
		t.Fatal("verify endpoint reported intact after tamper")
	}
	if body2.BrokenAtSeq == nil || *body2.BrokenAtSeq != 3 {
		t.Fatalf("brokenAtSeq = %v, want 3", body2.BrokenAtSeq)
	}
}

// TestLedgerListEndpoint: /api/ledger returns the committed entries for one
// symbol+horizon, newest first, scoped and limit-honoring.
func TestLedgerListEndpoint(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "Tesla")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.AppendLedger(ctx, store.LedgerEntry{
			PredictedAt: int64(100 + i), SymbolID: sym.ID, Horizon: md.H1d, BarTs: int64(100 + i),
			RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A 1w entry and another-symbol entry that must NOT appear in the 1d query.
	if _, err := st.AppendLedger(ctx, store.LedgerEntry{
		PredictedAt: 200, SymbolID: sym.ID, Horizon: md.H1w, BarTs: 200,
		RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendLedger(ctx, store.LedgerEntry{
		PredictedAt: 300, SymbolID: other.ID, Horizon: md.H1d, BarTs: 300,
		RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/ledger?symbol=AAPL&market=stocks&horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Symbol  string              `json:"symbol"`
		Horizon string              `json:"horizon"`
		Count   int                 `json:"count"`
		Entries []store.LedgerEntry `json:"entries"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Symbol != "AAPL" || body.Horizon != "1d" {
		t.Errorf("scope wrong: %+v", body)
	}
	if body.Count != 3 || len(body.Entries) != 3 {
		t.Fatalf("count = %d / %d entries, want 3 (1d, AAPL only)", body.Count, len(body.Entries))
	}
	// Newest first.
	if body.Entries[0].BarTs != 102 || body.Entries[2].BarTs != 100 {
		t.Errorf("order wrong: %d..%d, want 102..100", body.Entries[0].BarTs, body.Entries[2].BarTs)
	}
	// Each entry carries its chain fields.
	if body.Entries[0].EntryHash == "" || body.Entries[0].Seq == 0 {
		t.Errorf("entry missing chain fields: %+v", body.Entries[0])
	}

	// Unknown symbol → 404.
	res404, err := newClient(t).Get(srv.URL + "/api/ledger?symbol=NOPE&market=stocks")
	if err != nil {
		t.Fatal(err)
	}
	defer res404.Body.Close() //nolint:errcheck
	if res404.StatusCode != 404 {
		t.Errorf("unknown symbol status = %d, want 404", res404.StatusCode)
	}
}

// TestLedgerVerify_KeyFailureDoesNotLeakThePath: the verify payload is a public
// read, and the key-loading errors embed the key's absolute path (which carries
// the operator's home directory). A failure must be stated — silence would read
// as "anchored" — without disclosing where the key lives.
func TestLedgerVerify_KeyFailureDoesNotLeakThePath(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "readable-anchor.key")
	// Exposure is mode bits on Unix and an Everyone ACE on Windows; see
	// keyexposure_unix_test.go / keyexposure_windows_test.go.
	writeExposedKey(t, keyPath)
	srv, st := newLedgerServer(t, nil)
	t.Setenv(ledgeranchor.EnvKeyPath, keyPath) // world-readable → refused
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 4, 0.5)

	body := getLedgerVerify(t, srv, "")
	if body.Tamper.Anchoring.Wrote {
		t.Fatal("anchored with an unusable key")
	}
	if !strings.Contains(body.Tamper.Anchoring.Reason, "owner-only") {
		t.Errorf("reason = %q, want it to name the permission refusal", body.Tamper.Anchoring.Reason)
	}
	if strings.Contains(body.Tamper.Anchoring.Reason, keyPath) || strings.Contains(body.Tamper.Anchoring.Reason, "/") {
		t.Errorf("reason leaks a filesystem path: %q", body.Tamper.Anchoring.Reason)
	}
	if body.Tamper.LocalAnchorsReproduce || body.Tamper.ProvenAnteriorThroughSeq != nil {
		t.Error("claimed anteriority with no anchor")
	}
}

// A regenerated chain must not be able to read GREEN by outvoting its own
// honest history with a newer anchor.
//
// This is the gap an adversarial verifier found still open after the C2 fix:
// the summary checked only the NEWEST anchor, so an operator could delete the
// rows, re-append a fabricated chain, let the cadence write a fresh anchor over
// the fabricated head — which reproduces perfectly — and the page would return
// to green while the honest older anchor sat in the same table reporting its
// history was gone. A failing anchor is positive evidence of tampering and must
// dominate every newer one.
func TestLedgerVerify_FailingOlderAnchorDominatesANewerGoodOne(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// An honest chain, anchored.
	appendLedgerRows(t, st, sym.ID, 12, 0.5)
	if v := getLedgerVerify(t, srv, ""); !v.Tamper.Anchoring.Wrote {
		t.Fatalf("expected the first request to anchor the honest chain: %+v", v.Tamper.Anchoring)
	}

	// The operator regenerates history AND lets a fresh anchor land over the
	// fabricated chain — the case a newest-anchor-only check cannot see.
	for _, q := range []string{
		`DELETE FROM prediction_ledger`,
		`DELETE FROM meta WHERE k='ledger_verify_checkpoint'`,
		`DELETE FROM sqlite_sequence WHERE name='prediction_ledger'`,
	} {
		if _, err := st.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	appendLedgerRows(t, st, sym.ID, 12, 0.77)
	ver, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatalf("verify fabricated chain: %v", err)
	}
	// A zero-cadence policy stands in for "enough time passed" — the operator
	// only has to wait, which is not a defence.
	if _, wrote, reason, err := st.AnchorDue(ctx, ver, store.AnchorPolicy{}, time.Now()); err != nil || !wrote {
		t.Fatalf("anchor over fabricated chain: wrote=%v reason=%q err=%v", wrote, reason, err)
	}

	v := getLedgerVerify(t, srv, "")
	if v.Tamper.FailingAnchors < 1 {
		t.Fatalf("failingAnchors = %d — a regenerated chain read clean, so the summary is still checking only the newest anchor",
			v.Tamper.FailingAnchors)
	}
	if v.Tamper.LocalAnchorsReproduce {
		t.Error("claimed anteriority while a previously-signed anchor no longer reproduces")
	}
	if !strings.Contains(v.Tamper.Claim, "TAMPER EVIDENCE") {
		t.Errorf("claim does not lead with the tamper signal: %q", v.Tamper.Claim)
	}
}

// ── Finding A11: the verify routes must be bounded, not a DoS lever ─────────

// TestLedgerVerify_FullWalkRequiresAuth: the ?full=1 genesis walk is the
// expensive auditor path and must reject anonymous callers even when
// PublicReads is on; the incremental default stays public.
func TestLedgerVerify_FullWalkRequiresAuth(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 3, 0.5)

	for _, path := range []string{"/api/ledger/verify?full=1", "/api/ledger/anchors?full=1"} {
		res, err := newClient(t).Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close() //nolint:errcheck
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous %s: status = %d, want 401", path, res.StatusCode)
		}
	}
	// The incremental default remains a public read.
	res, err := newClient(t).Get(srv.URL + "/api/ledger/verify")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Errorf("anonymous incremental verify: status = %d, want 200", res.StatusCode)
	}
}

// TestLedgerVerify_ConcurrencyGuard: with the daemon-wide verification
// semaphore saturated, a verify request 429s immediately instead of queueing a
// CPU-bound chain walk — the exact resource-exhaustion lever of finding A11.
func TestLedgerVerify_ConcurrencyGuard(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 3, 0.5)

	for i := 0; i < ledgerVerifyConcurrency; i++ {
		ledgerVerifySem <- struct{}{}
	}
	defer func() {
		for i := 0; i < ledgerVerifyConcurrency; i++ {
			<-ledgerVerifySem
		}
	}()
	res := ledgerGet(t, srv, "/api/ledger/verify")
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("saturated semaphore: status = %d, want 429", res.StatusCode)
	}
	if res.Header.Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
}

// TestLedgerVerify_SyntheticChainCompletesAndDetectsTamper: a synthetic chain
// of a few thousand rows verifies well inside the request deadline via both
// paths, and a payload mutation is detected at the exact seq by the full walk.
func TestLedgerVerify_SyntheticChainCompletesAndDetectsTamper(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	const n = 3000
	appendLedgerRows(t, st, sym.ID, n, 0.5)

	start := time.Now()
	v := getLedgerVerify(t, srv, "?full=1")
	elapsed := time.Since(start)
	if !v.Intact || v.Count != n {
		t.Fatalf("intact/count = %v/%d, want true/%d", v.Intact, v.Count, n)
	}
	if elapsed > ledgerVerifyTimeout {
		t.Fatalf("full walk of %d rows took %s, exceeding the %s request deadline", n, elapsed, ledgerVerifyTimeout)
	}

	// Mutate one payload mid-chain; the full walk must break exactly there.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE prediction_ledger SET cal_prob=cal_prob+0.25 WHERE seq=1500`); err != nil {
		t.Fatal(err)
	}
	v2 := getLedgerVerify(t, srv, "?full=1")
	if v2.Intact {
		t.Fatal("full walk reported intact after payload mutation")
	}
	if v2.BrokenAtSeq == nil || *v2.BrokenAtSeq != 1500 {
		t.Fatalf("brokenAtSeq = %v, want 1500", v2.BrokenAtSeq)
	}
}
