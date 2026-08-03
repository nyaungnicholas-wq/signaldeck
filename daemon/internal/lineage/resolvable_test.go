package lineage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRepo builds a throwaway repository with exactly one commit and returns the
// directory and that commit's full sha.
func gitRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "f")
	run("commit", "-qm", "c")

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	sha = string(out[:40])
	return dir, sha
}

// A stamp naming a commit the repository really contains is the only case that
// may report true.
func TestRevisionResolvableAcceptsACommitTheRepositoryContains(t *testing.T) {
	dir, sha := gitRepo(t)
	if !revisionResolvable(context.Background(), dir, sha, false) {
		t.Fatalf("commit %s is in the repo but was reported unresolvable", sha)
	}
}

// The acceptance criterion for this item: a well-formed stamp that no longer
// resolves must be reported unresolved. Under the old build-time form this
// input produced true, because the build was clean and the string non-empty.
func TestRevisionResolvableRejectsAStampThatNoLongerResolves(t *testing.T) {
	dir, _ := gitRepo(t)
	const gone = "0123456789abcdef0123456789abcdef01234567" // 40 hex, never committed
	if revisionResolvable(context.Background(), dir, gone, false) {
		t.Fatal("a revision this repository does not contain was reported resolvable")
	}
}

// Dirty and empty stamps are unresolvable by construction. Preserving this is
// what keeps the dirty-build refusal intact.
func TestRevisionResolvableRejectsDirtyAndEmptyStamps(t *testing.T) {
	dir, sha := gitRepo(t)
	if revisionResolvable(context.Background(), dir, sha, true) {
		t.Error("a dirty build was reported resolvable")
	}
	if revisionResolvable(context.Background(), dir, "", false) {
		t.Error("an empty stamp was reported resolvable")
	}
	if revisionResolvable(context.Background(), dir, "abc123", false) {
		t.Error("a short non-commit stamp was reported resolvable")
	}
}

// Outside a checkout there is nothing to verify against, so the answer is false.
// Reporting true here would be the exact fail-open this field must not have.
func TestRevisionResolvableFailsClosedOutsideACheckout(t *testing.T) {
	_, sha := gitRepo(t)
	if revisionResolvable(context.Background(), t.TempDir(), sha, false) {
		t.Fatal("resolvable reported true from a directory that is not a checkout")
	}
}

// A caller that goes away, or a git that never answers, must not yield true.
func TestRevisionResolvableFailsClosedWhenVerificationCannotRun(t *testing.T) {
	dir, sha := gitRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if revisionResolvable(ctx, dir, sha, false) {
		t.Fatal("resolvable reported true although the check could not be performed")
	}
}
