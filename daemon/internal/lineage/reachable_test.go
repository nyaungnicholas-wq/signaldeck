// BuildReachable is the gate that catches a failure BuildModified cannot see: a
// CLEAN build carrying a real 40-hex commit that a later rebase or amend removed
// from the repository. Three such stamps put 946 rows into prediction_ledger on
// 2026-08-02, and the grader's revision gate then refused the whole directional
// family — permanently, because the rows never age out of its window.
//
// The asymmetry these tests pin: a proven absence must stop the daemon, and an
// UNPROVABLE answer must not. Refusing on "git could not be consulted" would
// take the deployed fleet down, since the sanctioned deploy path builds from a
// `git archive HEAD` extraction that has no .git at all.
package lineage

import (
	"context"
	"testing"
)

// withBuild forces the package's build-info singleton to a known state and
// restores it afterwards. readBuild() is called first so its sync.Once is spent
// and cannot overwrite what the test sets.
func withBuild(t *testing.T, r string, mod, ldf bool) {
	t.Helper()
	readBuild()
	oldRev, oldMod, oldLdf := rev, modified, fromLdflags
	rev, modified, fromLdflags = r, mod, ldf
	// The positive-resolution cache is keyed by revision|dirty|dir, but clear
	// it anyway so no case inherits another's proof.
	resolveMu.Lock()
	resolveKey = ""
	resolveMu.Unlock()
	t.Cleanup(func() {
		rev, modified, fromLdflags = oldRev, oldMod, oldLdf
		resolveMu.Lock()
		resolveKey = ""
		resolveMu.Unlock()
	})
}

func TestBuildReachableAcceptsACommitStillInTheRepository(t *testing.T) {
	dir, sha := gitRepo(t)
	withBuild(t, sha, false, false)
	ok, checked := BuildReachable(context.Background(), dir)
	if !checked {
		t.Fatal("a real checkout could be consulted but the answer was reported unchecked")
	}
	if !ok {
		t.Errorf("commit %s is in the repo but was reported unreachable", sha)
	}
}

// The 2026-08-02 case: clean build, well-formed commit, history rewritten under
// it. This is the one that must stop startup.
func TestBuildReachableRejectsACommitTheRepositoryNoLongerHas(t *testing.T) {
	dir, _ := gitRepo(t)
	withBuild(t, "3c096b33c1962cd0e5b771b6df9e6ec130bbcf58", false, false)
	ok, checked := BuildReachable(context.Background(), dir)
	if !checked {
		t.Fatal("git was available, so the absence was provable and must be reported as checked")
	}
	if ok {
		t.Error("a commit absent from the repository was reported reachable")
	}
}

// Outside a checkout the question is unanswerable. Reporting checked=true here
// would refuse to start every deployed binary.
func TestBuildReachableOutsideACheckoutIsUnprovableNotFalse(t *testing.T) {
	withBuild(t, "3c096b33c1962cd0e5b771b6df9e6ec130bbcf58", false, false)
	_, checked := BuildReachable(context.Background(), t.TempDir())
	if checked {
		t.Error("no checkout present, but the answer was reported as a proven verdict")
	}
}

// The sanctioned deploy path injects the commit via -ldflags and ships a tree
// with no .git. Its provenance is certain by construction, so it is never asked.
func TestBuildReachableSkipsTheDeployPathBuild(t *testing.T) {
	withBuild(t, "3c096b33c1962cd0e5b771b6df9e6ec130bbcf58", false, true)
	ok, checked := BuildReachable(context.Background(), t.TempDir())
	if checked {
		t.Error("an ldflags-stamped build was interrogated; it has no .git to answer with")
	}
	if !ok {
		t.Error("an ldflags-stamped build must not be reported unreachable")
	}
}

// A dirty or unstamped build is the OTHER gate's business. This one must not
// claim a proven verdict about it, or the two refusals would race to report
// different reasons for the same startup failure.
func TestBuildReachableDefersOnDirtyAndUnstampedBuilds(t *testing.T) {
	dir, sha := gitRepo(t)
	withBuild(t, sha, true, false)
	if _, checked := BuildReachable(context.Background(), dir); checked {
		t.Error("a dirty build was reported as a proven reachability verdict")
	}
	withBuild(t, "", false, false)
	if _, checked := BuildReachable(context.Background(), dir); checked {
		t.Error("an unstamped build was reported as a proven reachability verdict")
	}
}
