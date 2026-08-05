#!/usr/bin/env bash
# Freeze the graded corpus and the in-flight working tree so remediation cannot
# shift the ground it is standing on. Copy-only: nothing here mutates the repo
# or the live database.
#
# Usage: ops/freeze-corpus.sh [label]
set -euo pipefail

cd "$(dirname "$0")/.."
label="${1:-$(git rev-parse --short HEAD)}"
out="freeze/${label}"
mkdir -p "$out"

db="data/signaldeck.db"

# 1. Point-in-time copy of the database. VACUUM INTO is a read-only snapshot
#    that also folds in the WAL, so the copy is self-consistent.
rm -f "$out/signaldeck.frozen.db"
sqlite3 "$db" "VACUUM INTO '$out/signaldeck.frozen.db'"

# 2. The working tree, which is dirty and partly untracked. Both halves matter:
#    a patch alone would lose every untracked file in the fix-pack.
git diff HEAD > "$out/tracked.patch"
git status --porcelain > "$out/git-status.txt"
git rev-parse HEAD > "$out/git-head.txt"
git stash list > "$out/git-stashes.txt"
git ls-files --others --exclude-standard -z \
  | tar --null -T - -czf "$out/untracked.tar.gz"

# 3. Corpus row counts — the numbers every downstream claim is computed from.
{
  for t in predictions prediction_ledger prediction_outcomes forecasts \
           regime_outcomes universe_membership prereg_records grader_heartbeats; do
    printf '%-28s %s\n' "$t" "$(sqlite3 "$db" "select count(*) from $t;" 2>/dev/null || echo MISSING)"
  done
} > "$out/row-counts.txt"

# 4. The pre-registration chain head and the grader's registration status, which
#    is what determines whether anything in the corpus is legitimately graded.
sqlite3 "$db" \
  "select seq, ts, kind, spec_hash, entry_hash from prereg_records order by seq;" \
  > "$out/prereg-chain.txt"
sqlite3 "$db" \
  "select task, success, finished_at, grader_sha256, rows_evaluated, coalesce(error,'') from grader_heartbeats order by finished_at;" \
  > "$out/grader-heartbeats.txt"
python -c "import hashlib,sys;print(hashlib.sha256(open('tools/accuracy_registry.py','rb').read()).hexdigest())" \
  > "$out/grader-ondisk-sha256.txt"

# 5. Manifest last, so it covers everything above.
( cd "$out" && find . -type f ! -name MANIFEST.sha256 -print0 \
    | sort -z | xargs -0 sha256sum ) > "$out/MANIFEST.sha256"

echo "frozen -> $out"
sha256sum "$out/MANIFEST.sha256"
