#!/bin/bash
# ledger-provenance.sh --write | --check
#
# The published half of the guarantee the git hooks enforce locally.
#
# THE SPLIT. ops/githooks/{pre-rebase,reference-transaction} refuse to orphan a
# commit the evidence ledger names, but they are local config: a fresh clone has
# no protection until someone runs `git config core.hooksPath ops/githooks`, and
# nothing stops a force-push from a machine that never did. CI is the backstop
# — except CI has no ledger. data/ is gitignored and signaldeck.db is 4 GB, so
# the runner cannot ask the database anything.
#
# So the question the database can answer is answered HERE, on the machine that
# has it, and the answer is committed:
#
#   --write   reads the ledger and records every commit it references that is
#             clean, still resolvable, and ALREADY ON THE REMOTE.
#   --check   needs no database. It asserts every recorded commit is still
#             reachable from some ref. This is what CI runs.
#
# WHY ONLY PUBLISHED COMMITS. A revision stamped by the local daemon minutes ago
# may sit on an unpushed branch; CI would never find it and would fail on every
# run for a commit that was never wrong. So --write records a revision only once
# it is reachable from a remote-tracking ref, and it reports the ones it skipped
# rather than hiding them. The window before a commit is pushed belongs to the
# hooks; everything after it belongs to CI. Neither covers the other, and
# together they cover the whole life of a commit.
set -u

SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="${LEDGER_MANIFEST:-$SD/ops/ledger-revisions.txt}"
DB="${SIGNALDECK_DB:-$SD/data/signaldeck.db}"

usage() { echo "usage: $0 --write | --check" >&2; exit 2; }
[ $# -eq 1 ] || usage

# reachable REV — true when REV is reachable from ANY ref in this repository.
# `rev-list -1 REV --not --all` prints something only when it is not, which is
# the same primitive the reference-transaction hook uses, deliberately: the
# local guard and the published guard must not disagree about what "orphaned"
# means.
reachable() { [ -z "$(git -C "$SD" rev-list -1 "$1" --not --all 2>/dev/null)" ]; }

case "$1" in
--write)
  # shellcheck source=/dev/null
  . "$SD/ops/lib-portable.sh" 2>/dev/null || { echo "cannot source lib-portable.sh" >&2; exit 1; }
  [ -f "$DB" ] || { echo "no ledger at $DB" >&2; exit 1; }

  revs="$(sd_sqlite_read "$DB" "
    SELECT DISTINCT revision FROM prediction_ledger WHERE revision IS NOT NULL
    UNION
    SELECT DISTINCT revision FROM regime_outcomes  WHERE revision IS NOT NULL;")" || {
    echo "could not read $DB" >&2; exit 1; }

  recorded=0; unpushed=0; gone=0; dirty=0
  body="$(mktemp)"; trap 'rm -f "$body"' EXIT
  printf '%s\n' "$revs" | sort | while IFS= read -r rev; do
    case "$rev" in *+dirty | "") continue ;; esac
    [ "${#rev}" -eq 40 ] || continue
    git -C "$SD" cat-file -e "$rev^{commit}" 2>/dev/null || continue
    [ -n "$(git -C "$SD" branch -r --contains "$rev" 2>/dev/null)" ] || continue
    printf '%s\n' "$rev" >>"$body"
  done

  # Counts for the report are recomputed here rather than inside the pipe: the
  # while loop above runs in a subshell and its increments do not survive it.
  # That is the same subshell trap that made an earlier version of this report
  # print zeroes while the manifest filled up correctly.
  for rev in $(printf '%s\n' "$revs"); do
    case "$rev" in *+dirty) dirty=$((dirty + 1)); continue ;; "") continue ;; esac
    [ "${#rev}" -eq 40 ] || continue
    if ! git -C "$SD" cat-file -e "$rev^{commit}" 2>/dev/null; then
      gone=$((gone + 1))
    elif [ -z "$(git -C "$SD" branch -r --contains "$rev" 2>/dev/null)" ]; then
      unpushed=$((unpushed + 1))
    else
      recorded=$((recorded + 1))
    fi
  done

  {
    echo "# Commits the evidence ledger references. GENERATED — do not hand-edit."
    echo "#"
    echo "# Every sha below is named by at least one row in prediction_ledger or"
    echo "# regime_outcomes. If one stops being reachable, tools/accuracy_registry.py"
    echo "# can no longer attribute those rows and strips the verdict from the whole"
    echo "# predictor family, permanently. ops/ledger-provenance.sh --check enforces"
    echo "# that in CI; the git hooks in ops/githooks enforce it locally."
    echo "#"
    echo "# Regenerate on the machine holding data/signaldeck.db:"
    echo "#   bash ops/ledger-provenance.sh --write"
    echo "#"
    echo "# recorded=$recorded unpushed=$unpushed unresolvable=$gone dirty-stamped=$dirty"
    sort <"$body"
  } >"$MANIFEST"

  echo "wrote $MANIFEST"
  echo "  recorded (on remote, enforced by CI): $recorded"
  echo "  skipped, not yet pushed:              $unpushed"
  echo "  skipped, already unresolvable:        $gone"
  echo "  skipped, +dirty stamps:               $dirty"
  ;;

--check)
  [ -f "$MANIFEST" ] || { echo "no manifest at $MANIFEST" >&2; exit 1; }
  missing="$(mktemp)"; trap 'rm -f "$missing"' EXIT
  n=0
  while IFS= read -r rev; do
    case "$rev" in \#* | "") continue ;; esac
    n=$((n + 1))
    reachable "$rev" || printf '%s\n' "$rev" >>"$missing"
  done <"$MANIFEST"

  if [ -s "$missing" ]; then
    echo ""
    echo "LEDGER PROVENANCE BROKEN: commit(s) the evidence ledger references are"
    echo "no longer reachable from any ref in this repository."
    echo ""
    sed 's/^/  /' "$missing"
    cat <<'EOF'

Every ledger row naming these commits is now unattributable. The grader will
refuse a verdict for the whole predictor family, and because its window is
anchored to a fixed epoch, that refusal does not expire.

This is the 2026-08-02 failure reproduced: three clean builds rewritten out of
history, 946 rows stranded, recovery only via a chained pre-registration
amendment.

To fix: restore the commits (git push the branch that still has them, or
recover them from a reflog) and re-run. If they are genuinely unrecoverable,
file the correction rather than deleting lines from the manifest:
  go run -C daemon ./cmd/prereg-amend -kind revision-epoch-correction
EOF
    exit 1
  fi
  echo "ledger provenance OK — all $n recorded commit(s) still reachable"
  ;;

*) usage ;;
esac
