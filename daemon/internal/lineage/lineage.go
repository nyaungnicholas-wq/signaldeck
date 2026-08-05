package lineage

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Node kinds. Closed set — Link rejects anything else so a typo cannot mint
// a new namespace silently.
const (
	KindFeature        = "feature"
	KindDatasetVersion = "dataset_version"
	KindHypothesis     = "hypothesis"
	KindExperiment     = "experiment"
	KindModel          = "model"
	KindPrediction     = "prediction"
	KindTrade          = "trade"
	KindClaim          = "claim"
)

// Edge kinds. Same closed-set discipline.
const (
	EdgeGeneratedBy = "generated_by"
	EdgeTestedIn    = "tested_in"
	EdgeProduced    = "produced"
	EdgeTradedAs    = "traded_as"
	EdgeGradedBy    = "graded_by"
	EdgeEvidencedBy = "evidenced_by"
)

var nodeKinds = map[string]bool{
	KindFeature: true, KindDatasetVersion: true, KindHypothesis: true,
	KindExperiment: true, KindModel: true, KindPrediction: true,
	KindTrade: true, KindClaim: true,
}

var edgeKinds = map[string]bool{
	EdgeGeneratedBy: true, EdgeTestedIn: true, EdgeProduced: true,
	EdgeTradedAs: true, EdgeGradedBy: true, EdgeEvidencedBy: true,
}

// ValidNodeKind reports whether k is a known node kind.
func ValidNodeKind(k string) bool { return nodeKinds[k] }

// Edge is the write-side view of one lineage edge. CreatedAt defaults to now;
// MetaJSON is optional (RevMeta stamps the code version).
type Edge struct {
	SrcKind, SrcID string
	DstKind, DstID string
	EdgeKind       string
	CreatedAt      int64
	MetaJSON       string
}

// Link validates and idempotently upserts one edge. Unknown kinds, empty IDs
// and self-loops are errors — a lineage graph with junk endpoints is worse
// than none, because it answers traces confidently and wrongly.
func Link(ctx context.Context, st *store.Store, e Edge) error {
	if !nodeKinds[e.SrcKind] {
		return fmt.Errorf("lineage: unknown src kind %q", e.SrcKind)
	}
	if !nodeKinds[e.DstKind] {
		return fmt.Errorf("lineage: unknown dst kind %q", e.DstKind)
	}
	if !edgeKinds[e.EdgeKind] {
		return fmt.Errorf("lineage: unknown edge kind %q", e.EdgeKind)
	}
	if e.SrcID == "" || e.DstID == "" {
		return fmt.Errorf("lineage: empty node id (src=%q dst=%q)", e.SrcID, e.DstID)
	}
	if e.SrcKind == e.DstKind && e.SrcID == e.DstID {
		return fmt.Errorf("lineage: self-loop on %s:%s", e.SrcKind, e.SrcID)
	}
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().Unix()
	}
	return st.UpsertLineageEdge(ctx, store.LineageEdge{
		SrcKind: e.SrcKind, SrcID: e.SrcID,
		DstKind: e.DstKind, DstID: e.DstID,
		EdgeKind: e.EdgeKind, CreatedAt: e.CreatedAt, MetaJSON: e.MetaJSON,
	})
}

// ── code-version capture ─────────────────────────────────────────────────

// BuildRevMetaKey is the meta row RecordBuildRevision writes at startup.
const BuildRevMetaKey = "lineage_build_rev"

var (
	revOnce  sync.Once
	rev      string
	modified bool
	// fromLdflags records that the stamp came from the sanctioned deploy
	// path's -ldflags injection rather than an embedded vcs.revision. That
	// build has no .git to interrogate and needs none.
	fromLdflags bool
)

// ldflagsRev is set with -ldflags "-X ...lineage.ldflagsRev=<commit>" by the
// sanctioned deploy path (ops/signaldeck-ctl.sh deploy), which builds from a
// `git archive HEAD` extraction. That tree has no .git, so the toolchain can
// embed no vcs.revision — yet it is the ONLY build whose provenance is certain,
// because its contents are the commit by construction.
//
// It is deliberately a fallback, never an override: an embedded vcs.revision
// always wins, so this cannot be used to paint a clean commit onto a dirty
// working-tree build. It is also ignored unless it is a full 40-hex object id.
var ldflagsRev string

func readBuild() {
	revOnce.Do(func() {
		bi, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
		if rev == "" && isFullHex(ldflagsRev) {
			rev = ldflagsRev
			modified = false
			fromLdflags = true
		}
	})
}

// BuildReachable reports whether this build's commit still EXISTS in the
// checkout at dir. It answers a question BuildModified cannot: a build can be
// clean, carry a real 40-hex commit, and still be unattributable because that
// commit was rebased, amended, or dropped afterwards. History rewritten under a
// running daemon silently converts every row it writes into evidence the
// grader will refuse forever — that is exactly how 946 rows landed on
// 2026-08-02 under three clean stamps this repository no longer contains.
//
// checked reports whether the question could be answered at all. A proven NO
// (git ran, and the object is absent) is the only result that should stop
// anything: an unprovable answer must never be read as a refusal, the same
// posture revisionResolvableCached takes when it caches only a proven YES.
// Builds from the sanctioned deploy path are skipped outright — that tree is a
// `git archive HEAD` extraction with no .git, and its provenance is certain by
// construction rather than by lookup.
func BuildReachable(ctx context.Context, dir string) (ok, checked bool) {
	readBuild()
	if fromLdflags {
		return true, false
	}
	if rev == "" || modified {
		// Already the other gate's business, and not a question about history.
		return false, false
	}
	// Distinguish "no such commit" from "cannot ask". Without a checkout here
	// there is nothing to prove either way.
	probe, cancel := context.WithTimeout(ctx, revisionResolveTimeout)
	defer cancel()
	gitDir := exec.CommandContext(probe, "git", "rev-parse", "--git-dir")
	gitDir.Dir = dir
	if gitDir.Run() != nil {
		return false, false
	}
	return revisionResolvableCached(ctx, dir, rev, modified), true
}

// isFullHex reports whether s is a full 40-character lowercase-or-uppercase hex
// object id. Anything shorter or non-hex is not a commit and is discarded.
func isFullHex(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// BuildModified reports whether the binary was built from a DIRTY checkout
// (vcs.modified). A row stamped by such a binary names source that exists on
// no commit, so the grader treats it as unresolvable rather than as evidence.
func BuildModified() bool {
	readBuild()
	return modified
}

// RevisionStamp is the value written onto every row this build freezes: the
// bare commit when the checkout was clean, the commit with a "+dirty" suffix
// when it was not, and "" when no revision is embedded at all. The suffix is
// deliberately part of the STAMP rather than a separate column — a reader (or
// the grader) cannot look at the revision and miss that it is unresolvable.
func RevisionStamp() string {
	readBuild()
	if rev == "" {
		return ""
	}
	if modified {
		return rev + "+dirty"
	}
	return rev
}

// BuildRevision returns the binary's embedded VCS revision (vcs.revision from
// runtime/debug.ReadBuildInfo — no git exec at runtime). "" when the binary
// was built outside a checkout (e.g. `go test`).
func BuildRevision() string {
	readBuild()
	return rev
}

// revisionResolveTimeout bounds the git call so a wedged repository cannot hang
// the version endpoint. Expiry means "could not verify", which fails closed.
//
// 2s was too tight on Windows and made the field NON-DETERMINISTIC: six
// consecutive /api/version calls against the same binary and the same
// repository returned true, false, false, false, true, false. Spawning
// git.exe measured 370-930ms idle, and the daemon runs 97 workers against the
// same disk, so the cold-start spill past 2s is routine rather than rare.
// Failing closed then silently downgraded a VERIFIED build to unattributable,
// and the accuracy registry withholds verdicts on exactly that field — so a
// published record depended on a coin flip.
const revisionResolveTimeout = 10 * time.Second

// revisionResolveTTL caches a POSITIVE answer. The revision is fixed at build
// time and can only stop resolving through a history rewrite, so re-spawning
// git on every request bought nothing and was what exposed the race. A negative
// is never cached: it may be transient, and persisting it would keep claiming
// "unattributable" about a build that is fine.
const revisionResolveTTL = 5 * time.Minute

// The cache is keyed by the exact question asked. A bare boolean would answer
// "yes" for ANY revision once one had resolved, which is the fail-open this
// field exists to prevent.
var (
	resolveMu   sync.Mutex
	resolveKey  string
	resolveWhen time.Time
)

// RevisionResolvable reports whether the revision stamped into this binary still
// names a commit the repository actually contains — checked NOW, against git.
//
// The build-time facts alone cannot answer this. A clean build stamps a real
// commit, but that commit can later be rebased away, force-pushed over, or left
// on a deleted branch, and the binary would go on advertising a revision nobody
// can fetch. A stamp git cannot resolve is worth exactly as much as no stamp.
//
// It fails CLOSED, matching tools/accuracy_registry.py's revision_resolvable():
// when git is absent, the daemon runs outside a checkout, or the check times out,
// the answer is false. An unverifiable provenance claim must never be reported as
// a verified one — that is the whole point of the field.
func RevisionResolvable(ctx context.Context) bool {
	readBuild()
	return revisionResolvableCached(ctx, "", rev, modified)
}

// revisionResolvableCached is revisionResolvable with a positive-only, per-
// revision cache. Only a proven YES is remembered, and only for the revision
// that proved it.
func revisionResolvableCached(ctx context.Context, dir, revision string, dirty bool) bool {
	key := revision + "|" + strconv.FormatBool(dirty) + "|" + dir
	resolveMu.Lock()
	if resolveKey == key && time.Since(resolveWhen) < revisionResolveTTL {
		resolveMu.Unlock()
		return true
	}
	resolveMu.Unlock()

	if !revisionResolvable(ctx, dir, revision, dirty) {
		return false
	}
	resolveMu.Lock()
	resolveKey, resolveWhen = key, time.Now()
	resolveMu.Unlock()
	return true
}

// revisionResolvable is the testable core. dir is the directory to run git in;
// "" inherits the process working directory, which git walks up from to find the
// checkout. Any failure to prove resolvability returns false.
func revisionResolvable(ctx context.Context, dir, revision string, dirty bool) bool {
	// A dirty build names source that exists on no commit, and an empty or
	// malformed stamp names nothing at all. None is resolvable; none needs git.
	if revision == "" || dirty || !isFullHex(revision) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, revisionResolveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "cat-file", "-e", revision+"^{commit}")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// RecordBuildRevision persists the running build's VCS revision into the meta
// table at daemon startup, so the DB itself records which code versions have
// operated on it even before any edge is written.
func RecordBuildRevision(ctx context.Context, st *store.Store) error {
	// Every row this process freezes carries the same stamp, so "what code
	// produced this number" is answerable per-row rather than per-database.
	store.SetCodeRevision(RevisionStamp())
	r := BuildRevision()
	if r == "" {
		r = "unknown"
	}
	if modified {
		r += "+dirty"
	}
	return st.SetMeta(ctx, BuildRevMetaKey, r)
}

// RevMeta returns the meta_json research producers stamp on their edges:
// {"rev":"<sha>"} — or "" when no revision is embedded, so an unknown rev is
// absent rather than a fake value.
func RevMeta() string {
	r := BuildRevision()
	if r == "" {
		return ""
	}
	b, _ := json.Marshal(map[string]string{"rev": r})
	return string(b)
}
