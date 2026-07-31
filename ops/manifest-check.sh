#!/bin/bash
# Manifest check — every load-bearing path the documentation stands on must be
# tracked by git.
#
# The failure this exists to catch was real and silent: PREREGISTRATION.md,
# whose SHA-256 the daemon chains onto prediction rows, had zero commit history;
# daemon/.golangci.yml was untracked, so the golangci-lint action on the pushed
# tree ran with stock defaults while ci.yml carried a 12-line comment insisting
# it enforced errcheck/staticcheck/unused. A clone therefore did not contain the
# system the docs describe, and CI verified a configuration nobody had.
#
# The check then repeated that defect in miniature: tier 1 was a hand-maintained
# list of 7 paths and tier 2 read shell fences in exactly two documents, so any
# path a doc names in prose, in inline backticks, or in a markdown table was
# invisible to it. That is how repro/grading_protocol.csv stayed out of every
# clone — the registration file whose absence turns the one documented grading
# command from "ABORT: UNREGISTERED GRADER" into a full verdict table for a
# stranger — alongside daemon/internal/store/schemacontract.go, which supplies
# the verifySchema guard store.go uses to refuse a diverged database. A
# hand-listed manifest structurally cannot enforce this script's own stated
# rule; the scan has to follow the documents.
#
# Three tiers:
#   1. an explicit list of paths that must be tracked, no exceptions;
#   2. every repo-relative path named ANYWHERE in ANY tracked markdown file —
#      fenced blocks, inline-code spans, table cells, prose — that EXISTS in the
#      working tree. Existing-but-untracked is the defect; a path that does not
#      exist locally is a placeholder (e.g. `path/to/anchors-repo`) and is not
#      this check's business. A named directory must contain no untracked,
#      non-ignored files;
#   3. every member repro/MANIFEST.json names must be tracked, so the published
#      snapshot cannot document a file it does not ship.
#
# One-directional: it can only fail. It never edits, adds, or publishes.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

REQUIRED=(
  PREREGISTRATION.md
  REPRODUCE.md
  PREDICTION_PROCESS.md
  daemon/.golangci.yml
  daemon/internal/api/version.go
  ops/signaldeck-ctl.sh
  ops/manifest-check.sh
)

fail=0

# check <path> — a file must be tracked; a directory must hold no untracked,
# non-ignored file. Sets `fail` in the current shell, so never call it from a
# pipeline or command substitution.
check() {
  local p="${1%/}" stray missing f
  # A gitignored path is a local artifact (database, build output, .env, logs)
  # that the repo deliberately does not ship. Absent from a clone by design, so
  # not a manifest defect.
  git check-ignore -q -- "$p" 2>/dev/null && return
  # The predicate is HEAD, not the index. `git ls-files --error-unmatch` reads
  # the INDEX, so a staged-but-uncommitted file reports present while being
  # absent from every clone — the exact condition this script exists to catch,
  # and one it silently passed on itself. `git cat-file -e HEAD:<path>` asks the
  # only question that matters: does a fresh clone of HEAD contain this?
  if [ -d "$p" ]; then
    stray=$(git ls-files --others --exclude-standard -- "$p" 2>/dev/null | head -5)
    # Tracked-but-not-in-HEAD, as a set difference rather than one cat-file per
    # file: the index's view of the directory minus HEAD's view of it.
    missing=$(comm -23 \
      <(git ls-files -- "$p" 2>/dev/null | sort) \
      <(git ls-tree -r --name-only HEAD -- "$p" 2>/dev/null | sort) | head -5)
    if [ -z "$stray" ] && [ -z "$missing" ] && [ -n "$(git ls-files -- "$p" 2>/dev/null | head -1)" ]; then
      printf '  ✓ %s/\n' "$p"
    else
      printf '  ✗ %s/ — contains files absent from a clone of HEAD\n' "$p"
      # shellcheck disable=SC2086
      [ -n "$stray" ] && printf '      untracked: %s\n' $stray
      # shellcheck disable=SC2086
      [ -n "$missing" ] && printf '      not in HEAD: %s\n' $missing
      fail=1
    fi
  elif git cat-file -e "HEAD:$p" 2>/dev/null; then
    printf '  ✓ %s\n' "$p"
  else
    printf '  ✗ %s — exists in the tree but is NOT in HEAD (a clone lacks it)\n' "$p"
    fail=1
  fi
}

echo "── manifest tier 1: required paths ────────────────────────────────────"
for p in "${REQUIRED[@]}"; do check "$p"; done

echo "── manifest tier 2: every path named by any tracked markdown doc ───────"
# Extraction surfaces, all folded into one candidate stream: backticks and pipe
# characters are turned into separators, which flattens fenced blocks, inline
# code spans and table cells into plain tokens; surrounding prose comes along
# with them on purpose. The stream is then filtered by "does this path exist in
# the working tree", a far stronger filter than any guess about which markdown
# construct a path happened to be written in.
cands=$(git ls-files -z '*.md' '*.MD' \
  | xargs -0 -r sed -e 's/[`|]/ /g' -- 2>/dev/null \
  | grep -oE '[A-Za-z0-9_][A-Za-z0-9_./-]*' \
  | sort -u)
for p in $cands; do
  case "$p" in
    /*|*..*|*://*) continue;;
    */*) ;;    # path-shaped token
    *.*) ;;    # or a bare filename carrying an extension
    *) continue;;
  esac
  [ -e "$p" ] || continue   # placeholder or generated output, not a repo file
  check "$p"
done

echo "── manifest tier 3: repro/MANIFEST.json members are shipped ───────────"
if [ -f repro/MANIFEST.json ]; then
  # `python3` on Windows resolves to the Store alias stub, which prints an
  # install advert and exits 0 — the members list came back EMPTY and the check
  # reported a manifest naming nothing. An interpreter that is not there must
  # not read as a snapshot that documents nothing.
  py=""
  for c in python3 python; do
    if "$c" -c "import sys" >/dev/null 2>&1; then py="$c"; break; fi
  done
  if [ -z "$py" ]; then
    printf '  ✗ no working python found — tier 3 cannot be checked\n'
    fail=1
    py=false
  fi
  members=$("$py" - <<'PY'
import json, sys
try:
    d = json.load(open("repro/MANIFEST.json"))
except Exception as e:
    print("PARSE_ERROR " + str(e).replace("\n", " "))
    sys.exit(0)
for f in d.get("files", []):
    name = f.get("file") if isinstance(f, dict) else f
    if name:
        print("repro/" + str(name).lstrip("./"))
PY
)
  # A Windows python prints CRLF, and \r is not in IFS — every path would carry
  # a trailing \r, so `git cat-file` missed files that ARE in HEAD and the check
  # reported the snapshot unshipped. Strip it before splitting.
  members=${members//$'\r'/}
  case "$members" in
    PARSE_ERROR*)
      printf '  ✗ repro/MANIFEST.json is unparseable — %s\n' "${members#PARSE_ERROR }"
      fail=1;;
    "")
      printf '  ✗ repro/MANIFEST.json names no files — the snapshot documents nothing\n'
      fail=1;;
    *)
      for p in $members; do
        if git cat-file -e "HEAD:$p" 2>/dev/null; then
          printf '  ✓ %s\n' "$p"
        else
          printf '  ✗ %s — named by repro/MANIFEST.json but NOT in HEAD\n' "$p"
          fail=1
        fi
      done;;
  esac
else
  printf '  – repro/MANIFEST.json absent; nothing to assert\n'
fi

echo ""
if [ "$fail" = "0" ]; then
  echo "MANIFEST OK — a fresh clone contains every load-bearing path."
else
  echo "MANIFEST FAIL — the paths marked ✗ are in your working tree but not in a"
  echo "clone. Until they are committed, the published system is not the audited"
  echo "one. Fix by tracking them; never by deleting the reference."
fi
exit "$fail"
