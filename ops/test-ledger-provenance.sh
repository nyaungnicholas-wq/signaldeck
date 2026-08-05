#!/bin/bash
# Proves ops/ledger-provenance.sh --check still detects an orphaned commit.
#
# This runs in CI BEFORE the real check, for the reason the pre-publish-scan job
# already established: a checker that cannot demonstrate it still fails on a
# planted defect is not evidence when it passes. That is not hypothetical here.
# The pre-rebase hook shipped green against a unit test while silently reading a
# 26,689-row ledger as empty, because the sqlite3 CLI on that machine emitted
# CRLF and every 40-hex sha arrived 41 characters long. A green check proved
# nothing. So this plants a real orphan in a real repository and requires the
# checker to fail on it.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

pass=0
fail=0
ok()  { printf '  ok    %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL  %s\n' "$1"; fail=$((fail + 1)); }

command -v git >/dev/null 2>&1 || { echo "git not available"; exit 0; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
REPO="$TMP/repo"
mkdir -p "$REPO/ops"
cp "$SD/ops/ledger-provenance.sh" "$REPO/ops/"
cp "$SD/ops/lib-portable.sh" "$REPO/ops/"
cd "$REPO" || exit 1
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid
export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
git init -q .
echo base >f && git add -A && git commit -qm base
KEPT="$(git rev-parse HEAD)"

# A commit that will be orphaned: made on a branch, then the branch is deleted.
git checkout -q -b doomed
echo x >>f && git commit -qam doomed
ORPHAN="$(git rev-parse HEAD)"
git checkout -q -
git branch -q -D doomed

run() { bash ops/ledger-provenance.sh --check >/dev/null 2>&1; echo $?; }

# A manifest naming only reachable commits must pass.
printf '# header\n%s\n' "$KEPT" >ops/ledger-revisions.txt
[ "$(run)" = "0" ] && ok "passes when every recorded commit is reachable" \
                   || bad "failed on a manifest of reachable commits"

# The planted defect: a recorded commit that nothing references any more.
printf '# header\n%s\n%s\n' "$KEPT" "$ORPHAN" >ops/ledger-revisions.txt
[ "$(run)" = "1" ] && ok "fails when a recorded commit has been orphaned" \
                   || bad "did NOT detect an orphaned commit — the check is not evidence"

# The failure has to name the sha, or nobody can act on it.
out="$(bash ops/ledger-provenance.sh --check 2>&1)"
printf '%s' "$out" | grep -q "$ORPHAN" \
  && ok "names the orphaned commit" \
  || bad "the failure does not name the orphaned commit"

# Comments and blank lines are not shas.
printf '# just a comment\n\n' >ops/ledger-revisions.txt
[ "$(run)" = "0" ] && ok "tolerates a manifest of only comments" \
                   || bad "tripped on comments"

# A missing manifest is a setup error, not a silent pass: the whole point is
# that this file is committed, so its absence must be loud.
rm -f ops/ledger-revisions.txt
[ "$(run)" = "1" ] && ok "fails loudly when the manifest is missing" \
                   || bad "a missing manifest passed silently"

cd "$SD" || exit 1
printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
