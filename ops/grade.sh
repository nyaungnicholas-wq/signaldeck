#!/bin/sh
# The CONTAINER grader. Runs inside the image; POSIX sh, no bash, no git.
#
# WHY THIS EXISTS AND ops/accuracy-registry.sh DOES NOT RUN HERE
# --------------------------------------------------------------
# DEPLOY.md tells the operator to cron `ops/accuracy-registry.sh` on the
# deployment. That script cannot run in a container and never could: it is 680
# lines that shell out to git, rewrite README.md between markers, inject the
# rendered block into eight partials/ documents, and page Telegram. None of
# those things exist in the image (there is no .git, no checkout, no README to
# publish). It is the DEV-BOX job.
#
# NOT PORTED, ON PURPOSE: the DEPLOYMENT DRIFT gate accuracy-registry.sh runs
# before the grader (tools/deployment_drift.py, wired 2026-09-10). Two of its
# checks shell out to git, and although the image now carries git and this
# repository's COMMIT objects (see the build-provenance section below), it
# carries no trees and no blobs -- so `git show <rev>:<path>` and a
# path-filtered `git diff <base>..HEAD` still cannot run here. The container
# therefore applies one gate fewer than the dev box: a stale binary the dev-box
# publish refuses on is still graded here. Recorded so the divergence is known,
# not silent. Shipping trees and blobs would close it and would also put the
# whole source history in the image; that trade has not been made.
#
# The consequence of nobody noticing was severe, because /api/accuracy is
# deliberately fail-closed (internal/api/accuracy.go): an unreadable registry
# is 503 REFUSED, and a grader heartbeat older than GraderMaxAge (26h) is 503
# REFUSED_STALE. With no grader in the container, BOTH conditions hold forever
# -- so the honesty page, which is the product, was permanently dead on any
# container deployment.
#
# This script is only the part the HTTP publication gate actually reads:
# generate the registry, merge the selection-honesty verdicts, run the same
# collapse gate the handler runs, and record the heartbeat the handler checks.
# Same python, same binaries, same gate.
#
# It is stdlib-only by design. accuracy_registry.py, live_accuracy.py,
# grader_heartbeat.py and selection_honesty.py import nothing outside the
# standard library, so the image needs `apk add python3` and no pip at all.
#
# NEVER edit tools/accuracy_registry.py to make something here easier. Its
# sha256 is pinned in the pre-registration chain and it refuses to run when its
# own bytes change. That gate is correct: the code deciding verdicts must be
# the code the chain froze. Ship guards beside it, never inside it.

set -u

DB="${SIGNALDECK_DB:-/data/signaldeck.db}"
OUT="${SIGNALDECK_REGISTRY:-/data/accuracy_registry.json}"
TOOLS="${SIGNALDECK_TOOLS:-/app/tools}"
REPO="${SIGNALDECK_APP:-/app}"
PY="${SIGNALDECK_PYTHON:-python3}"
LOG="${SIGNALDECK_GRADE_LOG:-/data/logs/grade.log}"

mkdir -p "$(dirname "$LOG")" "$(dirname "$OUT")" 2>/dev/null

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*" | tee -a "$LOG"; }

refusal=""

# --- the protocol document must be the registered one ----------------------
# Ported verbatim in intent from ops/accuracy-registry.sh. A verdict graded
# under a protocol document that is not the one frozen on the chain is a
# verdict tied to nothing. This only ever REFUSES; it never re-pins a digest.
doc_hash=$("$PY" - "$REPO/PREREGISTRATION.md" <<'PY'
import hashlib, sys
try:
    print(hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest())
except Exception:
    print("")
PY
)
chain_hash=$("$PY" - "$DB" <<'PY'
import sqlite3, sys
try:
    con = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
    row = con.execute(
        "SELECT spec_hash FROM prereg_records WHERE kind = 'prereg-document' "
        "ORDER BY seq DESC LIMIT 1").fetchone()
    print(row[0] if row else "")
except Exception:
    print("")
PY
)
if [ -z "$doc_hash" ]; then
  refusal="PREREGISTRATION.md is not readable in the image at $REPO; the protocol document in force cannot be identified"
elif [ -z "$chain_hash" ]; then
  refusal="UNREGISTERED PROTOCOL DOCUMENT: the chain carries no prereg-document record, so PREREGISTRATION.md ($doc_hash) freezes nothing"
elif [ "$doc_hash" != "$chain_hash" ]; then
  refusal="UNREGISTERED PROTOCOL DOCUMENT: PREREGISTRATION.md hashes $doc_hash but the newest prereg-document record pins $chain_hash"
fi

# --- build provenance -------------------------------------------------------
# THE GATE THIS FILE'S HEADER SAYS IS MISSING, in the only form the image can
# honestly run. tools/deployment_drift.py cannot work here -- two of its checks
# shell out to git and there is no .git in the image -- so the container applied
# one gate fewer than the dev box, and a stale binary the dev-box publish
# refuses on was still graded here.
#
# This does not simulate that check. It answers a narrower question completely:
# are the bytes about to decide a verdict the ones the reviewed build contained?
# ops/docker-build.sh hashed them on the host, where git exists; this re-hashes
# them here and compares. A forged GIT_REV does not help, because the binding is
# to content rather than to a label.
#
# WHAT IT REFUSES TO CLAIM: that the manifest's revision was verified. The
# manifest binds BYTES, and it reports the revision as RECORDED with "not
# verifiable here" on every run, because a label the build host supplied is not
# evidence. Inventing resolvability there would be the same dishonesty as the
# fail-open gates removed elsewhere in this file.
#
# SEPARATELY, AND IT IS A DIFFERENT QUESTION: the grader's revision gate asks
# whether each historical forecast row names a commit that exists, and it asks
# that of every row, not of this image. It answered no to all of them here --
# no git, no objects -- so apply_revision_gate() stripped the verdict from every
# directional and structural row and this script published a registry with no
# verdicts in it. ops/docker-build.sh now packs this repository's commit objects
# and the image unpacks them into /app/.git, which is the path the pinned grader
# already looks in. That is evidence, not an assertion: git objects are
# content-addressed, so a revision nobody committed still does not resolve.
MANIFEST="${SIGNALDECK_BUILD_MANIFEST:-/app/build-manifest.json}"
if [ -z "$refusal" ]; then
  manifest_out=$("$PY" "$TOOLS/build_manifest.py" verify --manifest "$MANIFEST" \
                    --root "$REPO" --bin-root / 2>&1)
  manifest_status=$?
  log "$manifest_out"
  case "$manifest_status" in
    0) ;;
    # 1 = the check RAN and the bytes disagree. That names a file and is an
    #     accusation about this deployment, so it is worded as one.
    1) refusal="BUILD MANIFEST MISMATCH: the artifacts in this image are not the ones the reviewed build pinned -- ${manifest_out}. The grade was not run. This is a deployment fault, NOT a finding about any model" ;;
    # Anything else = the check could not run at all: no manifest, an unusable
    # one, a missing tool, a python that died. Same withholding, different
    # sentence -- an outage published as an accusation is its own dishonesty.
    *) refusal="CHECK UNAVAILABLE: the build manifest could not be verified (exit $manifest_status), so this image cannot be bound to any reviewed source -- ${manifest_out}. The grade was not run. This is a check outage, NOT a finding about any model" ;;
  esac
fi

# --- survivorship bound, best effort ---------------------------------------
# Measured before the grade so the bound published beside today's numbers was
# computed against today's universe. It can only ever WIDEN the disclosed
# bound, so a network failure must not block grading.
if [ -z "$refusal" ]; then
  if ! "$PY" "$TOOLS/backfill_delistings.py" --survivorship-bound >>"$LOG" 2>&1; then
    log "survivorship-bound measurement failed (non-fatal); the grade proceeds"
  fi
fi

# --- the grade -------------------------------------------------------------
# STAGED. $OUT is the file the daemon serves -- internal/api/accuracy.go
# loadRegistry() os.ReadFile's it on EVERY request -- so writing the grader's
# raw output straight there published ungated rows for as long as the checks
# below took to run, and selection_honesty.py --merge reopening the same path
# "w" meant a reader could also catch it truncated. Build in $STAGE, check
# $STAGE, and replace $OUT with one atomic rename at the end.
STAGE="${OUT}.staging"
rm -f "$STAGE"

if [ -z "$refusal" ]; then
  if ! "$PY" "$TOOLS/accuracy_registry.py" --db "$DB" --json "$STAGE" >>"$LOG" 2>&1; then
    refusal="accuracy_registry.py exited non-zero; see $LOG"
  fi
fi

# --- selection honesty ------------------------------------------------------
# Merges its verdicts into the registry. Its exit code is deliberately NOT
# propagated: it refuses individual ROWS, which is a finding to publish, not a
# reason to withhold the whole registry.
#
# BUT A RETURN CODE CANNOT TELL THOSE APART FROM A CRASH. main() returns
# `1 if refused else 0`, and an unhandled exception exits 1 too, so a traceback
# on row three and a clean run that refused row three were the same byte here --
# and the half-merged JSON was published either way. The exit code is still
# ignored on purpose; the ARTIFACT is what gets checked, by publication_gate.py,
# which asserts every directional row with breadth tallies actually carries an
# honesty block from the right source with the right keys. A crash cannot answer
# that yes.
if [ -z "$refusal" ]; then
  "$PY" "$TOOLS/selection_honesty.py" --json "$STAGE" --db "$DB" --merge >>"$LOG" 2>&1 \
    || log "selection_honesty exited non-zero (row refusals OR a crash -- the gate below decides which)"

  if ! pubgate_out=$("$PY" "$TOOLS/publication_gate.py" --registry "$STAGE" 2>&1); then
    log "$pubgate_out"
    refusal="CHECK UNAVAILABLE: the graded artifact is incomplete, so it was not published -- ${pubgate_out}. This is a post-processing failure, NOT a finding about any model"
  else
    log "$pubgate_out"
  fi
fi

# --- the collapse gate ------------------------------------------------------
# The same gate internal/api runs, so the served surface and the on-disk
# registry cannot disagree.
#
# EXIT 2 NOW WITHHOLDS. It used to publish, "exactly like the handler", on the
# argument that refusing on a failed read would wedge publication shut on a
# transient DB error. Both ends were reversed together: internal/api now answers
# REFUSED_UNAVAILABLE on the same condition. The argument was never wrong about
# transient errors, it was wrong about everything ELSE that exits 2 -- a missing
# collapsecheck binary, a renamed flag, a bad --db path -- every one of which
# silently un-wires the gate for good. In THIS file that risk is higher than on
# the dev box, not lower: collapsecheck is a binary baked into the image, so a
# build that ships without it would have published forever and said nothing.
#
# The wording keeps a check outage and a measured collapse apart, because
# publishing the first as the second invents a scientific verdict.
if [ -z "$refusal" ]; then
  collapse_out=$(collapsecheck --db "$DB" --registry "$STAGE" 2>>"$LOG")
  case "$?" in
    1) refusal="publication gate: $collapse_out" ;;
    0) ;;
    *) refusal="CHECK UNAVAILABLE: the collapsed-cross-section gate could not be evaluated, so the figures are withheld WITHOUT having been judged. This is a check outage, NOT a finding about any model; see $LOG" ;;
  esac
fi

# --- atomic publication -----------------------------------------------------
# One rename, after every gate has passed and only then.
if [ -z "$refusal" ]; then
  if ! mv -f "$STAGE" "$OUT"; then
    refusal="CHECK UNAVAILABLE: every gate passed but the staged registry could not be moved into place; the previously published registry is untouched"
  fi
fi

# --- the heartbeat the daemon reads ----------------------------------------
# Without this the handler cannot distinguish "graded cleanly" from "nothing
# has run for a week", and GraderMaxAge turns the second into REFUSED_STALE.
if [ -z "$refusal" ]; then
  # CHECKED, AND FATAL WHEN IT FAILS. This call used to be unchecked and was
  # followed unconditionally by `log "grade OK"; exit 0`, so the script reported
  # a clean grade whether or not the one record the daemon reads got written.
  # That is not a cosmetic gap: /api/accuracy answers REFUSED_STALE when this
  # heartbeat is older than GraderMaxAge (26h), so a silently failed write means
  # the registry on disk carries today's verdicts while the API refuses them --
  # and the container's own header says a permanently dead honesty page IS the
  # failure this file was written to end.
  if ! "$PY" "$TOOLS/grader_heartbeat.py" --success --db "$DB" --registry "$OUT" \
      --grader "$TOOLS/accuracy_registry.py" >>"$LOG" 2>&1; then
    log "GRADE INCOMPLETE: registry published but the success heartbeat could not be written; /api/accuracy will serve REFUSED_STALE over it"
    exit 1
  fi
  log "grade OK"
  exit 0
fi

log "GRADE REFUSED: $refusal"
# Best-effort by necessity -- there is nowhere left to escalate -- but no longer
# silent: if even the refusal cannot be recorded, say so in the log and keep the
# non-zero exit.
"$PY" "$TOOLS/grader_heartbeat.py" --failure --error "$refusal" --db "$DB" \
  --registry "$OUT" --grader "$TOOLS/accuracy_registry.py" >>"$LOG" 2>&1 \
  || log "and the failure heartbeat could not be written either; the daemon will fall back to REFUSED_STALE on age alone"
rm -f "$STAGE"
exit 1
