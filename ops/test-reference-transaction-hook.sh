#!/bin/bash
# Exercises ops/githooks/reference-transaction through REAL git verbs.
#
# Deliberately not by piping stdin at the hook. The pre-rebase hook's own unit
# test passed green while the hook was reading a 26,689-row ledger and reporting
# nothing referenced, because the harness environment differed from the real
# one. So every case here runs an actual `git commit --amend`, `git reset
# --hard` or `git branch -D` against a repo with the hook installed the way a
# developer installs it, and asserts on what happened to the refs afterwards.
#
# The two directions that matter: it must BLOCK the verbs pre-rebase never sees,
# and it must stay invisible during ordinary work. A hook that taxes every
# commit gets deleted, and then it protects nothing.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

pass=0
fail=0
ok()   { printf '  ok    %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  FAIL  %s\n' "$1"; fail=$((fail + 1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected $2, got $3)"; fi; }

command -v git >/dev/null 2>&1     || { echo "git not available"; exit 0; }
command -v sqlite3 >/dev/null 2>&1 || { echo "sqlite3 not available"; exit 0; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
REPO="$TMP/repo"

setup() { # setup LEDGER_REVISION_PLACEHOLDER — fresh repo, hook installed
  rm -rf "$REPO"
  mkdir -p "$REPO/ops/githooks" "$REPO/data"
  cp "$SD/ops/githooks/reference-transaction" "$REPO/ops/githooks/"
  cp "$SD/ops/lib-portable.sh" "$REPO/ops/"
  chmod +x "$REPO/ops/githooks/reference-transaction"
  cd "$REPO" || exit 1
  export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid
  export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
  git init -q .
  echo base >f && git add -A && git commit -qm base
  BASE="$(git rev-parse HEAD)"
  echo a >>f && git commit -qam a
  MID="$(git rev-parse HEAD)"
  echo b >>f && git commit -qam b
  TIP="$(git rev-parse HEAD)"
  # Installed only now, so the setup commits above are not themselves policed.
  git config core.hooksPath ops/githooks
}

ledger() { # ledger REVISION
  rm -f data/signaldeck.db
  sqlite3 data/signaldeck.db "
    CREATE TABLE prediction_ledger (predicted_at INTEGER, revision TEXT);
    CREATE TABLE regime_outcomes  (ts INTEGER, revision TEXT);
    INSERT INTO prediction_ledger VALUES (1785456000, '$1');"
}

# ── ordinary work must be untouched ───────────────────────────────────────────
setup; ledger "$MID"
echo c >>f
git commit -qam c 2>/dev/null
check "a normal commit is allowed (fast-forward)" "3" \
      "$(git rev-list --count "$BASE"..HEAD)"

# ── the case pre-rebase never sees: amend ─────────────────────────────────────
setup; ledger "$TIP"
git commit -q --amend -m "amended" >/dev/null 2>&1
check "git commit --amend is BLOCKED when it orphans a referenced tip" "$TIP" \
      "$(git rev-parse HEAD)"

setup; ledger "$MID"
git commit -q --amend -m "amended" >/dev/null 2>&1
check "amend is allowed when the referenced commit survives as an ancestor" "amended" \
      "$(git log -1 --format=%s)"

# ── hard reset ────────────────────────────────────────────────────────────────
setup; ledger "$TIP"
git reset -q --hard "$BASE" >/dev/null 2>&1
check "git reset --hard is BLOCKED when it rewinds past a referenced commit" "$TIP" \
      "$(git rev-parse HEAD)"

setup; ledger "$BASE"
git reset -q --hard "$BASE" >/dev/null 2>&1
check "reset is allowed when the referenced commit is the new tip" "$BASE" \
      "$(git rev-parse HEAD)"

# ── branch deletion ───────────────────────────────────────────────────────────
setup; ledger "$TIP"
git branch keepit "$TIP" >/dev/null 2>&1
git checkout -q "$BASE" 2>/dev/null
git branch -q -D master >/dev/null 2>&1 || git branch -q -D main >/dev/null 2>&1
check "deleting a branch is allowed while another ref still holds the commit" "0" \
      "$(git cat-file -e "$TIP^{commit}" 2>/dev/null; echo $?)"

setup; ledger "$TIP"
git checkout -q -b other "$BASE" 2>/dev/null
git branch -D master >/dev/null 2>&1 || git branch -D main >/dev/null 2>&1
check "deleting the ONLY branch holding a referenced commit is BLOCKED" "1" \
      "$(git for-each-ref --format='%(refname)' | grep -qE 'refs/heads/(master|main)$'; echo $?; )"

# ── unprovable and irrelevant cases must never block ──────────────────────────
setup; ledger "${TIP}+dirty"
git commit -q --amend -m "amended" >/dev/null 2>&1
check "a +dirty reference does not block an amend" "amended" "$(git log -1 --format=%s)"

setup; ledger "3c096b33c1962cd0e5b771b6df9e6ec130bbcf58"
git commit -q --amend -m "amended" >/dev/null 2>&1
check "an already-unresolvable reference does not block an amend" "amended" \
      "$(git log -1 --format=%s)"

setup; ledger "$TIP"; rm -f data/signaldeck.db
git commit -q --amend -m "amended" >/dev/null 2>&1
check "no ledger database does not block an amend" "amended" "$(git log -1 --format=%s)"

setup; ledger "$TIP"
SIGNALDECK_ALLOW_HISTORY_REWRITE=1 git commit -q --amend -m "amended" >/dev/null 2>&1
check "the documented override lets the amend through" "amended" "$(git log -1 --format=%s)"

# ── the refusal has to be actionable ──────────────────────────────────────────
setup; ledger "$TIP"
out="$(git commit --amend -m "amended" 2>&1)"
if printf '%s' "$out" | grep -q "$TIP"; then
  ok "the refusal names the offending commit"
else
  bad "the refusal does not name the offending commit"
fi

cd "$SD" || exit 1
printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
