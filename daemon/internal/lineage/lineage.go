package lineage

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
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
		}
	})
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
