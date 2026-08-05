#!/bin/bash
# Exercises ops/githooks/pre-rebase against a throwaway repo and ledger.
#
# The hook is the only thing standing between a routine `git rebase` and the
# 2026-08-02 failure: three clean builds rewritten out of history, 946 ledger
# rows orphaned, the directional family's verdict stripped permanently, and a
# chained pre-registration amendment needed to recover. So both directions are
# pinned here — it must REFUSE a real collision, and it must ALLOW everything it
# cannot prove, because a hook that blocks on "I could not check" gets deleted
# within a day and then protects nothing.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOOK="$SD/ops/githooks/pre-rebase"
LIB="$SD/ops/lib-portable.sh"

pass=0
fail=0
check() { # check NAME EXPECTED_RC ACTUAL_RC
  if [ "$2" -eq "$3" ]; then
    printf '  ok    %s\n' "$1"
    pass=$((pass + 1))
  else
    printf '  FAIL  %s (expected rc=%s, got rc=%s)\n' "$1" "$2" "$3"
    fail=$((fail + 1))
  fi
}

command -v git >/dev/null 2>&1 || { echo "git not available"; exit 0; }
command -v sqlite3 >/dev/null 2>&1 || { echo "sqlite3 not available"; exit 0; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
REPO="$TMP/repo"
mkdir -p "$REPO/ops/githooks" "$REPO/data"
cp "$HOOK" "$REPO/ops/githooks/pre-rebase"
cp "$LIB" "$REPO/ops/lib-portable.sh"
chmod +x "$REPO/ops/githooks/pre-rebase"

cd "$REPO" || exit 1
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid
export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
git init -q .
echo base >f && git add -A && git commit -qm base
BASE="$(git rev-parse HEAD)"
echo a >>f && git commit -qam a
MID="$(git rev-parse HEAD)"
echo b >>f && git commit -qam b

mkledger() { # mkledger REVISION
  rm -f data/signaldeck.db
  sqlite3 data/signaldeck.db "
    CREATE TABLE prediction_ledger (predicted_at INTEGER, revision TEXT);
    CREATE TABLE regime_outcomes  (ts INTEGER, revision TEXT);
    INSERT INTO prediction_ledger VALUES (1785456000, '$1');"
}

run() { sh ops/githooks/pre-rebase "$@" >/dev/null 2>&1; echo $?; }

# The collision: MID is inside BASE..HEAD and the ledger names it.
mkledger "$MID"
check "refuses a rebase that rewrites a referenced commit" 1 "$(run "$BASE" HEAD)"

# Same ledger, but the range stops short of MID — nothing is lost.
check "allows a rebase whose range excludes the referenced commit" 0 "$(run "$MID" HEAD)"

# BASE is the upstream itself, never rewritten by BASE..HEAD.
mkledger "$BASE"
check "allows when only the upstream commit is referenced" 0 "$(run "$BASE" HEAD)"

# A dirty stamp is already unattributable; rewriting under it strands nothing.
mkledger "${MID}+dirty"
check "allows when the reference is a +dirty stamp" 0 "$(run "$BASE" HEAD)"

# A stamp naming a commit that does not resolve cannot be made worse.
mkledger "3c096b33c1962cd0e5b771b6df9e6ec130bbcf58"
check "allows when the referenced commit is already unresolvable" 0 "$(run "$BASE" HEAD)"

# Unprovable cases must never block.
mkledger "$MID"
rm -f data/signaldeck.db
check "allows when there is no ledger database" 0 "$(run "$BASE" HEAD)"

mkledger "$MID"
check "allows under the documented override" 0 \
  "$(SIGNALDECK_ALLOW_HISTORY_REWRITE=1 sh ops/githooks/pre-rebase "$BASE" HEAD >/dev/null 2>&1; echo $?)"

# No upstream argument means git is not telling us what would be rewritten.
check "allows when git passes no upstream" 0 "$(run)"

# The refusal must NAME the commit, or nobody can act on it.
mkledger "$MID"
out="$(sh ops/githooks/pre-rebase "$BASE" HEAD 2>&1)"
if printf '%s' "$out" | grep -q "$MID"; then
  printf '  ok    the refusal names the offending commit\n'
  pass=$((pass + 1))
else
  printf '  FAIL  the refusal does not name the offending commit\n'
  fail=$((fail + 1))
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
