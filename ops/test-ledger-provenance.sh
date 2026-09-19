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
MAIN="$(git rev-parse --abbrev-ref HEAD)"
KEPT="$(git rev-parse HEAD)"

# A commit that will be orphaned: made on a branch, then the branch is deleted.
git checkout -q -b doomed
echo x >>f && git commit -qam doomed
ORPHAN="$(git rev-parse HEAD)"
git checkout -q -
git branch -q -D doomed

# Fixture additions for cases 6-20
FAKE=0123456789abcdef0123456789abcdef01234567
GIT_COMMITTER_DATE='2026-01-01T00:00:00Z' GIT_AUTHOR_DATE='2026-01-01T00:00:00Z' git commit -q --allow-empty -m old
OLD="$(git rev-parse HEAD)"
git commit -q --allow-empty -m new
NEW="$(git rev-parse HEAD)"
git update-ref refs/remotes/origin/main HEAD
git checkout -q -b local-only
git commit -q --allow-empty -m local
LOCAL="$(git rev-parse HEAD)"
git checkout -q "$MAIN"
export SIGNALDECK_DB="$REPO/ledger.db"
. ops/lib-portable.sh
sd_sqlite "$SIGNALDECK_DB" "CREATE TABLE prediction_ledger (revision TEXT); CREATE TABLE regime_outcomes (revision TEXT); INSERT INTO prediction_ledger VALUES ('$KEPT'),('$OLD'),('$NEW'),('$LOCAL'),('$ORPHAN'),('$FAKE'),('$KEPT+dirty'),(NULL); INSERT INTO regime_outcomes VALUES ('$NEW'),('$FAKE');"

run() { bash ops/ledger-provenance.sh --check >/dev/null 2>&1; echo $?; }
rc() { bash ops/ledger-provenance.sh "$@" >/dev/null 2>&1; echo $?; }

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

# Case 6: fails when a recorded commit does not exist at all
L="fails when a recorded commit does not exist at all"
printf '# header\n%s\n%s\n' "$KEPT" "$FAKE" >ops/ledger-revisions.txt
[ "$(run)" = "1" ] && ok "$L" || bad "$L"

# Case 7: names the nonexistent commit
L="names the nonexistent commit"
out="$(bash ops/ledger-provenance.sh --check 2>&1)"
printf '%s' "$out" | grep -qF -- "$FAKE" \
  && ok "$L" || bad "$L"

# Case 8: tolerates CRLF line endings in the manifest
L="tolerates CRLF line endings in the manifest"
printf '# header\r\n%s\r\n' "$KEPT" >ops/ledger-revisions.txt
[ "$(run)" = "0" ] && ok "$L" || bad "$L"

# A missing manifest is a setup error, not a silent pass: the whole point is
# that this file is committed, so its absence must be loud.
rm -f ops/ledger-revisions.txt
[ "$(run)" = "1" ] && ok "fails loudly when the manifest is missing" \
                   || bad "a missing manifest passed silently"

# Case 9: --write records exactly the clean, resolvable, remote-reachable revisions
L="--write records exactly the clean, resolvable, remote-reachable revisions"
wrc="$(rc --write)"
manifest_shas="$(grep -E '^[0-9a-f]{40}$' ops/ledger-revisions.txt | LC_ALL=C sort | tr '\n' ' ')"
expected_shas="$(printf '%s\n' "$KEPT" "$OLD" "$NEW" | LC_ALL=C sort | tr '\n' ' ')"
[ "$wrc" = "0" ] && [ "$manifest_shas" = "$expected_shas" ] && ok "$L" || bad "$L"

# Case 10: --write header counts recorded=3 unpushed=2 unresolvable=1 dirty-stamped=1
L="--write header counts recorded=3 unpushed=2 unresolvable=1 dirty-stamped=1"
grep -qF '# recorded=3 unpushed=2 unresolvable=1 dirty-stamped=1' ops/ledger-revisions.txt \
  && ok "$L" || bad "$L"

# Case 11: --write names every skipped revision with its reason
L="--write names every skipped revision with its reason"
out="$(bash ops/ledger-provenance.sh --write 2>&1)"
printf '%s' "$out" | grep -qF -- "$LOCAL  not published (no remote branch or tag reaches it); a local ref does" \
  && printf '%s' "$out" | grep -qF -- "$ORPHAN  not published (no remote branch or tag reaches it); NO ref reaches it" \
  && printf '%s' "$out" | grep -qF -- "$FAKE  unresolvable" \
  && printf '%s' "$out" | grep -qF -- "$KEPT+dirty  +dirty stamp" \
  && ok "$L" || bad "$L"

# Case 12: --check passes on the freshly written manifest
L="--check passes on the freshly written manifest"
[ "$(run)" = "0" ] && ok "$L" || bad "$L"

# Case 13: --diff is OK right after --write
L="--diff is OK right after --write"
out="$(bash ops/ledger-provenance.sh --diff 2>&1)"
last="$(printf '%s\n' "$out" | tail -n 1)"
case "$last" in
  "LEDGER MANIFEST OK: recorded=3 unrecorded=0"*) ok "$L" ;;
  *) bad "$L" ;;
esac

# Case 13b: --diff tolerates CRLF line endings in the manifest. The daily
# watchdog reads the working copy on a core.autocrlf=true machine; a CR that
# survived into the sha match would report every recorded commit as unrecorded.
# TEETH ONLY IN CI. MSYS grep on the Windows box strips the CR itself, so this
# case passes there against the broken order too; ubuntu's grep keeps it and
# fails. Do not read a local pass as proof the ordering is right.
L="--diff tolerates CRLF line endings in the manifest"
printf '# header\r\n%s\r\n%s\r\n%s\r\n' "$KEPT" "$OLD" "$NEW" >ops/ledger-revisions.txt
last="$(bash ops/ledger-provenance.sh --diff 2>&1 | tail -n 1)"
case "$last" in
  "LEDGER MANIFEST OK: recorded=3 unrecorded=0"*) ok "$L" ;;
  *) bad "$L" ;;
esac

# Case 14: --diff is STALE when a revision on the remote for 7+ days is unrecorded
L="--diff is STALE when a revision on the remote for 7+ days is unrecorded"
printf '# header\n%s\n' "$KEPT" >ops/ledger-revisions.txt
out="$(bash ops/ledger-provenance.sh --diff 2>&1)"
last="$(printf '%s\n' "$out" | tail -n 1)"
case "$last" in
  "LEDGER MANIFEST STALE: 1 revision"*) ok "$L" ;;
  *) bad "$L" ;;
esac

# Case 15: --diff names the unrecorded revisions
L="--diff names the unrecorded revisions"
printf '%s' "$out" | grep -qF -- "$OLD  unrecorded  age=" \
  && printf '%s' "$out" | grep -qF -- "$NEW  unrecorded  age=0d" \
  && ok "$L" || bad "$L"

# Case 16: --diff is OK, not stale, when the only unrecorded revision is younger than 7 days
L="--diff is OK, not stale, when the only unrecorded revision is younger than 7 days"
printf '# header\n%s\n%s\n' "$KEPT" "$OLD" >ops/ledger-revisions.txt
out="$(bash ops/ledger-provenance.sh --diff 2>&1)"
last="$(printf '%s\n' "$out" | tail -n 1)"
case "$last" in
  "LEDGER MANIFEST OK: recorded=2 unrecorded=1"*) ok "$L" ;;
  *) bad "$L" ;;
esac

# Case 17: --diff honours LEDGER_STALE_DAYS=0
L="--diff honours LEDGER_STALE_DAYS=0"
out="$(LEDGER_STALE_DAYS=0 bash ops/ledger-provenance.sh --diff 2>&1)"
last="$(printf '%s\n' "$out" | tail -n 1)"
case "$last" in
  "LEDGER MANIFEST STALE:"*) ok "$L" ;;
  *) bad "$L" ;;
esac

# Case 18: --diff is BROKEN when a recorded revision is no longer recordable
L="--diff is BROKEN when a recorded revision is no longer recordable"
printf '# header\n%s\n%s\n%s\n%s\n' "$KEPT" "$OLD" "$NEW" "$LOCAL" >ops/ledger-revisions.txt
out="$(bash ops/ledger-provenance.sh --diff 2>&1)"
last="$(printf '%s\n' "$out" | tail -n 1)"
case "$last" in
  "LEDGER MANIFEST BROKEN: 1 recorded"*) 
    printf '%s' "$out" | grep -qF -- "$LOCAL  no longer recordable" && ok "$L" || bad "$L"
    ;;
  *) bad "$L" ;;
esac

# Case 19: --diff exits 2 when the database is missing
L="--diff exits 2 when the database is missing"
[ "$(SIGNALDECK_DB="$REPO/nope.db" rc --diff)" = "2" ] && ok "$L" || bad "$L"

# Case 20: --diff exits 2 when the manifest is missing
L="--diff exits 2 when the manifest is missing"
rm -f ops/ledger-revisions.txt
[ "$(rc --diff)" = "2" ] && ok "$L" || bad "$L"


# Case 21: a commit published ONLY by a pushed TAG is recorded.
# The published test used to be `git branch -r --contains` alone, so a keep TAG
# -- the remedy the history-rewrite note prescribes for an orphaned ledger
# revision -- read as unpublished and was excluded from the manifest.
L="a commit reachable only from a pushed tag is recorded"
git init -q --bare "$REPO/../origin.git"
git remote add origin "$REPO/../origin.git" 2>/dev/null
git push -q origin "$MAIN" 2>/dev/null
git checkout -q -b tagonly
git commit -q --allow-empty -m tagonly
TAGONLY="$(git rev-parse HEAD)"
git tag -a keep/tagonly -m keep
git push -q origin refs/tags/keep/tagonly 2>/dev/null
git checkout -q "$MAIN"
git branch -q -D tagonly
sd_sqlite "$SIGNALDECK_DB" "INSERT INTO prediction_ledger VALUES ('$TAGONLY');" 2>/dev/null || \
  sd_sqlite "$SIGNALDECK_DB" "INSERT INTO prediction_ledger VALUES ('$TAGONLY');"
bash ops/ledger-provenance.sh --write >/dev/null 2>&1
grep -qF "$TAGONLY" ops/ledger-revisions.txt && ok "$L" || bad "$L"
cd "$SD" || exit 1
printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
