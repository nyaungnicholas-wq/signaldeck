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
# checks shell out to git against the deployed revision and there is no .git
# here, so it cannot produce a verdict in the image. The container therefore
# applies one gate fewer than the dev box: a stale binary the dev-box publish
# refuses on is still graded here. Recorded so the divergence is known, not silent.
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
if [ -z "$refusal" ]; then
  if ! "$PY" "$TOOLS/accuracy_registry.py" --db "$DB" --json "$OUT" >>"$LOG" 2>&1; then
    refusal="accuracy_registry.py exited non-zero; see $LOG"
  fi
fi

# --- selection honesty ------------------------------------------------------
# Merges its verdicts into the registry. Its exit code is deliberately NOT
# propagated: it refuses individual ROWS, which is a finding to publish, not a
# reason to withhold the whole registry.
if [ -z "$refusal" ]; then
  "$PY" "$TOOLS/selection_honesty.py" --json "$OUT" --db "$DB" --merge >>"$LOG" 2>&1 \
    || log "selection_honesty merged with a non-zero exit (row-level refusals are expected)"
fi

# --- the collapse gate ------------------------------------------------------
# The same gate internal/api runs, so the served surface and the on-disk
# registry cannot disagree. FAILS OPEN exactly like the handler: only an
# explicit exit 1 refuses. Exit 2 means undetermined, and refusing on a failed
# read would wedge publication shut on a transient DB error rather than on
# evidence.
if [ -z "$refusal" ]; then
  collapse_out=$(collapsecheck --db "$DB" --registry "$OUT" 2>>"$LOG")
  case "$?" in
    1) refusal="publication gate: $collapse_out" ;;
    0) ;;
    *) log "collapse gate undetermined -- publishing, as /api/accuracy does" ;;
  esac
fi

# --- the heartbeat the daemon reads ----------------------------------------
# Without this the handler cannot distinguish "graded cleanly" from "nothing
# has run for a week", and GraderMaxAge turns the second into REFUSED_STALE.
if [ -z "$refusal" ]; then
  "$PY" "$TOOLS/grader_heartbeat.py" --success --db "$DB" --registry "$OUT" \
    --grader "$TOOLS/accuracy_registry.py" >>"$LOG" 2>&1
  log "grade OK"
  exit 0
fi

log "GRADE REFUSED: $refusal"
"$PY" "$TOOLS/grader_heartbeat.py" --failure --error "$refusal" --db "$DB" \
  --registry "$OUT" --grader "$TOOLS/accuracy_registry.py" >>"$LOG" 2>&1
exit 1
