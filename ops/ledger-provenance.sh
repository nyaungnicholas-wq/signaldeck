#!/bin/bash
# ledger-provenance.sh --write | --check | --diff
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
#   --diff    reads the ledger, recomputes what --write would record, and
#             compares with the committed manifest. It is the daily watchdog:
#             ops/check-grader-health.ps1 runs it and treats exit 1 as a
#             warning (stale or broken) and exit 2 as unhealthy (cannot run).
#
# WHY ONLY PUBLISHED COMMITS. A revision stamped by the local daemon minutes ago
# may sit on an unpushed branch; CI would never find it and would fail on every
# run for a commit that was never wrong. So --write records a revision only once
# it is reachable from a remote-tracking ref, and it reports the ones it skipped
# rather than hiding them. The window before a commit is pushed belongs to the
# hooks; everything after it belongs to CI. Neither covers the other, and
# together they cover the whole life of a commit.
#
# STALENESS. A revision that has been on the remote for LEDGER_STALE_DAYS (default
# 7) but is not yet in the manifest means the weekly chore was missed. The
# manifest went 36 days and 101 revisions stale, 2026-08-04 to 2026-09-09, with
# CI green throughout. --diff flags this as STALE so the chore cannot slip again.
set -u

SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="${LEDGER_MANIFEST:-$SD/ops/ledger-revisions.txt}"
DB="${SIGNALDECK_DB:-$SD/data/signaldeck.db}"
STALE_DAYS="${LEDGER_STALE_DAYS:-7}"

usage() { echo "usage: $0 --write | --check | --diff" >&2; exit 2; }
[ $# -eq 1 ] || usage

# reachable REV — true when REV exists as a commit AND is reachable from ANY ref.
# `git rev-list` on a missing object exits 128 with empty stdout, which the old
# test read as "reachable". A fresh CI clone after a history rewrite has the
# rewritten commits ABSENT, not orphaned, so --check would pass on exactly the
# failure it exists to catch. Require cat-file first.
reachable() {
  git -C "$SD" cat-file -e "$1^{commit}" 2>/dev/null &&
  [ -z "$(git -C "$SD" rev-list -1 "$1" --not --all 2>/dev/null)" ]
}

# published_refs_contain REV -- is REV reachable from something that exists ON
# THE REMOTE, by branch OR by tag?
#
# This used to be `git branch -r --contains` alone, which is branches only. A
# commit preserved by a pushed TAG therefore read as unpublished and was
# excluded from the manifest -- and a keep TAG is exactly what
# audits/2026-09-10-history-rewrite-note.md prescribes for an orphaned ledger
# revision, so the recorder could not record the output of its own remedy.
# Measured 2026-09-11 on b84670c9: pushed as refs/tags/keep/ledger-revision-*,
# resolvable in a fresh clone, still filed under "not on a remote-tracking ref".
# --check never had this gap: rev-list --not --all counts tags.
#
# A LOCAL-only tag must not count, and locally a fetched tag is indistinguishable
# from a local one, so the remote tag list comes from ls-remote. --write is the
# only caller: it already needs the database and runs on the dev box. ls-remote
# failing (offline) leaves REMOTE_TAGS empty and this degrades to the old
# branches-only answer, which under-records rather than over-records.
REMOTE_TAGS=""
published_refs_contain() {
  [ -n "$(git -C "$SD" branch -r --contains "$1" 2>/dev/null)" ] && return 0
  [ -n "$REMOTE_TAGS" ] || return 1
  local t
  for t in $(git -C "$SD" tag --contains "$1" 2>/dev/null); do
    case " $REMOTE_TAGS " in *" $t "*) return 0 ;; esac
  done
  return 1
}

# classify_revisions — shared by --write and --diff.
# Reads distinct revisions from the ledger DB, classifies each, and fills:
#   $1 = path to temp file for recorded body (one sha per line, LC_ALL=C sorted)
#   $2 = path to temp file for skipped lines (each line: "<sha>  <reason>")
# Sets globals: recorded, unpushed, gone, dirty
classify_revisions() {
  local body_file="$1"
  local skipped_file="$2"

  recorded=0; unpushed=0; gone=0; dirty=0

  # Read all revisions, sort once, then iterate without a subshell.
  local revs
  revs="$(sd_sqlite_read "$DB" "
    SELECT DISTINCT revision FROM prediction_ledger WHERE revision IS NOT NULL
    UNION
    SELECT DISTINCT revision FROM regime_outcomes  WHERE revision IS NOT NULL;")" || return 1

  # Use a while loop fed by a here-string to avoid subshell.
  local rev
  while IFS= read -r rev; do
    case "$rev" in
      *+dirty)
        dirty=$((dirty + 1))
        printf '%s  +dirty stamp\n' "$rev" >>"$skipped_file"
        continue
        ;;
      "") continue ;;
    esac
    [ "${#rev}" -eq 40 ] || continue

    if ! git -C "$SD" cat-file -e "$rev^{commit}" 2>/dev/null; then
      gone=$((gone + 1))
      printf '%s  unresolvable\n' "$rev" >>"$skipped_file"
      continue
    fi

    if ! published_refs_contain "$rev"; then
      unpushed=$((unpushed + 1))
      local orphan_check
      orphan_check="$(git -C "$SD" rev-list -1 "$rev" --not --all 2>/dev/null)"
      if [ -z "$orphan_check" ]; then
        printf '%s  not published (no remote branch or tag reaches it); a local ref does\n' "$rev" >>"$skipped_file"
      else
        printf '%s  not published (no remote branch or tag reaches it); NO ref reaches it (orphan: the next git gc prunes it)\n' "$rev" >>"$skipped_file"
      fi
      continue
    fi

    recorded=$((recorded + 1))
    printf '%s\n' "$rev" >>"$body_file"
  done <<EOF
$(printf '%s\n' "$revs" | LC_ALL=C sort)
EOF

  # Sort the body file (already sorted from the loop, but ensure)
  LC_ALL=C sort -o "$body_file" "$body_file"
  return 0
}

case "$1" in
--write)
  # shellcheck source=/dev/null
  . "$SD/ops/lib-portable.sh" 2>/dev/null || { echo "cannot source lib-portable.sh" >&2; exit 1; }
  [ -f "$DB" ] || { echo "no ledger at $DB" >&2; exit 1; }
  # Tags that actually exist on the remote, for published_refs_contain. Best
  # effort: offline leaves this empty and the test falls back to branches only.
  REMOTE_TAGS="$(git -C "$SD" ls-remote --tags origin 2>/dev/null \
    | awk "{print \$2}" | sed -e "s|^refs/tags/||" -e "s|\^{}$||" | sort -u | tr "\n" " ")"

  body="$(mktemp)"
  skipped="$(mktemp)"
  trap 'rm -f "$body" "$skipped"' EXIT

  classify_revisions "$body" "$skipped" || { echo "could not read $DB" >&2; exit 1; }

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
    cat "$body"
  } >"$MANIFEST"

  echo "wrote $MANIFEST"
  echo "  recorded (on remote, enforced by CI):   $recorded"
  echo "  skipped, not on a remote-tracking ref:  $unpushed"
  echo "  skipped, already unresolvable:          $gone"
  echo "  skipped, +dirty stamps:                 $dirty"

  if [ -s "$skipped" ]; then
    echo "  skipped revisions:"
    sed 's/^/    /' "$skipped"
  fi
  ;;

--check)
  [ -f "$MANIFEST" ] || { echo "no manifest at $MANIFEST" >&2; exit 1; }
  missing="$(mktemp)"
  trap 'rm -f "$missing"' EXIT
  n=0
  while IFS= read -r rev; do
    # Strip CR for CRLF manifests (core.autocrlf=true on Windows)
    rev="${rev%$'\r'}"
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

--diff)
  # All output to stdout, nothing to stderr.
  # shellcheck source=/dev/null
  . "$SD/ops/lib-portable.sh" 2>/dev/null || { echo "LEDGER MANIFEST ERROR: cannot source lib-portable.sh"; exit 2; }
  # Tags that actually exist on the remote, for published_refs_contain. Best
  # effort: offline leaves this empty and the test falls back to branches only.
  REMOTE_TAGS="$(git -C "$SD" ls-remote --tags origin 2>/dev/null \
    | awk "{print \$2}" | sed -e "s|^refs/tags/||" -e "s|\^{}$||" | sort -u | tr "\n" " ")"
  [ -f "$DB" ] || { echo "LEDGER MANIFEST ERROR: no ledger at $DB"; exit 2; }
  [ -f "$MANIFEST" ] || { echo "LEDGER MANIFEST ERROR: no manifest at $MANIFEST"; exit 2; }

  body="$(mktemp)"
  skipped="$(mktemp)"
  manifest_shas="$(mktemp)"
  unrecorded="$(mktemp)"
  gone_file="$(mktemp)"
  trap 'rm -f "$body" "$skipped" "$manifest_shas" "$unrecorded" "$gone_file"' EXIT

  classify_revisions "$body" "$skipped" || { echo "LEDGER MANIFEST ERROR: could not read $DB"; exit 2; }

  # Manifest shas: strip CR BEFORE matching, or a CRLF checkout (core.autocrlf)
  # matches nothing and every recorded sha reports as unrecorded — a false STALE.
  tr -d '\r' <"$MANIFEST" | grep -E '^[0-9a-f]{40}$' | LC_ALL=C sort -u >"$manifest_shas"

  # body is already sorted unique from classify_revisions
  # unrecorded = body - manifest_shas
  LC_ALL=C comm -13 "$manifest_shas" "$body" >"$unrecorded"
  # gone = manifest_shas - body
  LC_ALL=C comm -23 "$manifest_shas" "$body" >"$gone_file"

  manifest_count=$(wc -l <"$manifest_shas" | tr -d ' ')
  unrecorded_count=$(wc -l <"$unrecorded" | tr -d ' ')
  gone_count=$(wc -l <"$gone_file" | tr -d ' ')

  # Count stale unrecorded revisions
  stale=0
  now=$(date +%s)
  while IFS= read -r sha; do
    [ -z "$sha" ] && continue
    commit_time=$(git -C "$SD" log -1 --format=%ct "$sha" 2>/dev/null)
    if [ -n "$commit_time" ]; then
      age_days=$(( (now - commit_time) / 86400 ))
      if [ "$age_days" -ge "$STALE_DAYS" ]; then
        stale=$((stale + 1))
      fi
    fi
  done <"$unrecorded"

  echo "manifest $MANIFEST: recorded=$manifest_count unrecorded=$unrecorded_count stale=$stale no-longer-recordable=$gone_count stale-days=$STALE_DAYS"

  # Print unrecorded lines with age
  while IFS= read -r sha; do
    [ -z "$sha" ] && continue
    commit_time=$(git -C "$SD" log -1 --format=%ct "$sha" 2>/dev/null)
    if [ -n "$commit_time" ]; then
      age_days=$(( (now - commit_time) / 86400 ))
      echo "  $sha  unrecorded  age=${age_days}d"
    else
      echo "  $sha  unrecorded  age=unknown"
    fi
  done <"$unrecorded"

  # Print gone lines
  while IFS= read -r sha; do
    [ -z "$sha" ] && continue
    echo "  $sha  no longer recordable"
  done <"$gone_file"

  # Final verdict
  if [ "$gone_count" -gt 0 ]; then
    echo "LEDGER MANIFEST BROKEN: $gone_count recorded commit(s) no longer on a remote-tracking ref or no longer resolve; do NOT regenerate over them, restore the commits or file a revision-epoch correction (see --check)"
    exit 1
  elif [ "$stale" -gt 0 ]; then
    echo "LEDGER MANIFEST STALE: $stale revision(s) cited by the ledger and on the remote for $STALE_DAYS+ days are not recorded; run: bash ops/ledger-provenance.sh --write, then commit ops/ledger-revisions.txt"
    exit 1
  else
    echo "LEDGER MANIFEST OK: recorded=$manifest_count unrecorded=$unrecorded_count stale-days=$STALE_DAYS"
    exit 0
  fi
  ;;

*) usage ;;
esac