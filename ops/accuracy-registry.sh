#!/bin/bash
# Continuous validation — re-grade every predictor's claim against its live record.
#
# Runs daily and read-only. The point is not the report, it is the alert: a predictor
# whose live accuracy falls below its claim should announce itself rather than wait to
# be noticed. Silence here means every shipping number is still supported.

set -uo pipefail

# Repo root, derived from this script's own location rather than hardcoded.
#
# This was "/Users/natalienyaung/claude code/signaldeck" — a path from the Mac
# this project was developed on. After the move it resolved nowhere, so the
# grader could not run AT ALL, and the README kept serving the refusal it had
# recorded on 2026-07-29 as though it were current. A stale refusal is worse
# than a loud failure: it looks like the honesty machinery working.
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# report_uncommitted_docs names the generated files this run left MODIFIED, and
# says what that costs.
#
# It is not housekeeping. Every surface this job regenerates is TRACKED, so a
# successful run always leaves the worktree dirty. Until 2026-08-26 both deploy
# paths refused ANY dirty tree, so this job manufactured a daily deploy blocker:
# measured 2026-08-21, it regenerated nine files at 18:33, a rebuild at 19:09
# stamped f5f6b0a+dirty, and the daemon stayed DOWN until they were committed.
# ops/signaldeck-ctl.sh build_from_head now exempts exactly the paths in
# ops/generated-docs.txt (they cannot reach the binary -- it builds from
# `git archive HEAD`); a HAND-built binary still gets a "+dirty" vcs stamp that
# cmd/signaldeckd/main.go refuses to start on.
#
# The job cannot commit for the operator -- an unattended commit of published
# numbers is its own problem -- but it can stop the next deploy being a mystery.
# Since 2026-08-26 ops/signaldeck-ctl.sh no longer refuses on dirt confined to
# exactly these paths (they cannot reach the binary; it builds from `git archive
# HEAD`). The list lives in ops/generated-docs.txt -- ONE spelling, shared with
# the deploy check, because two copies of one list is how they drift apart.
report_uncommitted_docs() {
  local dirty
  # shellcheck disable=SC2046 -- word-splitting the pathspecs is intended; no
  # listed path contains whitespace, and the allowlist header says exact paths.
  dirty="$(cd "$SD" && git status --porcelain -- $(grep -Ev '^[[:space:]]*(#|$)' "$SD/ops/generated-docs.txt" 2>/dev/null) 2>/dev/null)"
  [ -n "$dirty" ] || return 0
  echo "NOTE: this run regenerated tracked documents and left them UNCOMMITTED:"
  echo "$dirty" | sed 's/^/  /'
  echo "ops/signaldeck-ctl.sh deploy/launch proceed past exactly these paths (the"
  echo "binary builds from git archive HEAD). A HAND-built binary still stamps"
  echo "+dirty and REFUSES TO START -- signaldeckd exited 1 on that on 2026-08-21."
  echo "Commit them when convenient."
}

# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"

# python3 is not on PATH under Git Bash on Windows; shasum is a Perl script that
# ships with macOS and is often absent elsewhere. Resolve both, or fail loudly.
#
# `command -v python3` is NOT sufficient on Windows: there is a Microsoft Store
# "app execution alias" stub at that name which resolves fine and then prints
# "Python was not found" to stdout and exits 0. Detection has to RUN the thing.
PY=""
for cand in python3 python py; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import sys' >/dev/null 2>&1; then
    PY="$cand"; break
  fi
done
if [ -z "$PY" ]; then
  echo "accuracy-registry: no working python on PATH (tried python3, python, py)" >&2
  exit 1
fi

# Windows Python defaults to cp1252 for file I/O, and every document this script
# reads (README.md, PREREGISTRATION.md, the registry JSON) is UTF-8 with em
# dashes and arrows in it. Without this the grader dies in a decode error while
# reading its own output. Harmless on macOS/Linux, which are already UTF-8.
export PYTHONUTF8=1
export PYTHONIOENCODING=utf-8
sha256_of() {
  if command -v shasum   >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then sha256sum   "$1" | awk '{print $1}'
  else "$PY" -c "import hashlib,sys;print(hashlib.sha256(open(sys.argv[1],'rb').read()).hexdigest())" "$1"
  fi
}
LOG="$SD/logs/accuracy-registry.log"
OUT="$SD/data/accuracy_registry.json"
PREV="$SD/data/accuracy_registry.prev.json"

PREV_BACKUP="$SD/data/.accuracy_registry.prev.json.bak"
STDERR_CAPTURE="$(mktemp)"
trap 'rm -f "$STDERR_CAPTURE"' EXIT

# notify_remote "message" — H9: fan critical registry events out beyond the Mac
# using the same env-configured transports the daemon's internal/notify uses
# (daemon/.env). Best-effort: missing config or a curl failure never fails the
# run, and the secrets never hit the log (curl output is discarded).
notify_remote() {
  local msg="$1" env="$SD/daemon/.env" tok chat disc hook
  [ -f "$env" ] || return 0
  tok=$(sed -n 's/^SIGNALDECK_TELEGRAM_BOT_TOKEN=//p' "$env" | tail -1)
  chat=$(sed -n 's/^SIGNALDECK_TELEGRAM_CHAT_ID=//p' "$env" | tail -1)
  disc=$(sed -n 's/^SIGNALDECK_DISCORD_WEBHOOK=//p' "$env" | tail -1)
  hook=$(sed -n 's/^SIGNALDECK_WEBHOOK_URL=//p' "$env" | tail -1)
  if [ -n "$tok" ] && [ -n "$chat" ]; then
    curl -sS -m 10 -X POST "https://api.telegram.org/bot${tok}/sendMessage" \
      --data-urlencode "chat_id=${chat}" --data-urlencode "text=${msg}" >/dev/null 2>&1
  fi
  if [ -n "$disc" ]; then
    curl -sS -m 10 -H 'Content-Type: application/json' \
      -d "$("$PY" -c 'import json,sys; print(json.dumps({"content": sys.argv[1][:1900]}))' "$msg")" \
      "$disc" >/dev/null 2>&1
  fi
  if [ -n "$hook" ]; then
    curl -sS -m 10 -H 'Content-Type: application/json' \
      -d "$("$PY" -c 'import json,sys,time; print(json.dumps({"title":"SignalDeck accuracy","body":sys.argv[1],"kind":"accuracy","ts":int(time.time())}))' "$msg")" \
      "$hook" >/dev/null 2>&1
  fi
}

# The timestamp the registry carried BEFORE this run. Used below as a freshness
# assertion: a grader that exits 0 but leaves `generated` unmoved did not grade
# anything, and republishing its rows would be publishing yesterday's numbers
# under today's banner.
generated_of() {
  "$PY" -c "
import json,sys
try:
    print(json.load(open(sys.argv[1])).get('generated') or '')
except Exception:
    print('')
" "$1"
}
before_generated=$(generated_of "$OUT")

# Keep the previous run's registry so verdict TRANSITIONS (not steady states)
# can alert below (H9). PREV is clobbered before the grader runs, so stash the
# pre-run copy: if the grader refuses, PREV must be restored or the next
# successful run diffs against itself and reports no transitions.
[ -f "$PREV" ] && cp -f "$PREV" "$PREV_BACKUP"
[ -f "$OUT" ] && cp -f "$OUT" "$PREV"

echo "──────── $(date '+%Y-%m-%dT%H:%M:%S') ────────" >> "$LOG"

refusal_reason=""

# ENGINE LIVENESS — asserted here, in the publishing path, BEFORE the grader, and
# read straight out of SQLite rather than out of the daemon. The research loop's
# own preflight and read-back guards are properties of one binary's control flow;
# a stale build narrates "searched a 48-rule grid — NOTHING survived" with an
# empty ledger and nothing downstream notices. This is the same reason the grader
# is pinned rather than trusted. It can only suppress publication — it never
# writes a judgment row and never repairs a ledger.
"$PY" "$SD/tools/research_liveness.py" --db "$SD/data/signaldeck.db" \
  > "$STDERR_CAPTURE" 2>&1
liveness_status=$?
cat "$STDERR_CAPTURE" >> "$LOG"
if [ "$liveness_status" -ne 0 ]; then
  refusal_reason="research-loop liveness check failed (exit $liveness_status) — a narrated grid search left no verifiable judgment record, or a pre-registered forecast kind has never frozen a forecast and gave no refusal; the grader was not run"
fi

# EXTERNAL-TIMESTAMP LIVENESS. Anchors are SIGNED locally by the daemon and are
# supposed to be PUBLISHED to a third-party git repo — "a digest sitting in a
# third party's git history is the only evidence an operator who holds the
# signing key cannot fabricate after the fact" (ops/anchor-publish.sh).
#
# Measured 2026-08-12: the signing half worked (ledger_anchors held 10 rows,
# newest 2026-08-10) while anchor-publish.sh had not run since 2026-07-27 —
# nothing invokes it from anywhere — and NOTHING measured the gap. Sixteen days
# of anchors existed only on this machine, carrying none of the guarantee the
# project publicly claims for them.
#
# It runs HERE because this is the daily task that already exists and already
# runs the sibling liveness check, so the finding lands without waiting for a
# new scheduled task to be registered. --emit-dq-event puts it in the
# data-quality stream on the day it happens.
#
# DELIBERATELY NON-BLOCKING: it does NOT set refusal_reason. Unpublished anchors
# make the record less externally verifiable, but they do not make the graded
# numbers wrong, and suppressing the registry over it would withhold an honest
# track record to punish a missing git push.
"$PY" "$SD/tools/anchor_liveness.py" --db "$SD/data/signaldeck.db" --emit-dq-event \
  > "$STDERR_CAPTURE" 2>&1
anchor_liveness_status=$?
cat "$STDERR_CAPTURE" >> "$LOG"
if [ "$anchor_liveness_status" -ne 0 ]; then
  echo "WARN: external anchor timestamping is not current (exit $anchor_liveness_status) — see above; registry still published" >> "$LOG"
fi

# DEPLOYMENT DRIFT — is every mechanism the pre-registration chain CLAIMS
# observable in the live database? Every other honesty gate reads the tree at
# HEAD; this one reads the ROWS and asks whether the binary that wrote them is
# the one the chain describes: matched-null coverage, the judgment ledger, the
# frozen quarantine and its digest, the holdout-era constant in the deployed
# revision, no undeployed daemon source, and the per-row revision stamp.
#
# Its docstring has said "runs in the publishing path before the grader" since
# 2026-07-30. Measured 2026-09-09: nothing invoked it — not this script, not a
# scheduled task, not CI; only its own tests. A gate that exists and never runs
# is the A4/A13 shape again.
#
# BLOCKING on exit 1 — the tool's own contract, and the same failure class the
# ENGINE LIVENESS check above refuses on: a registry graded as though a frozen
# null or a blind era existed, when the running binary never implemented
# either, is not a live number. Advisory would turn the stderr it prints
# ("Publication must be refused") into a lie in a log nobody reads. It has
# refused on a false diagnosis three times (371551a, A21 in
# audits/2026-08-12-reaudit.md, 6f02590). Two of those fixes shipped a
# regression test with them; the A21 fix (3318028) shipped none, so
# tools/test_deployment_drift_wiring.py now pins the revision-boot signal that
# commit introduced, as well as the shape of this block. It measured 6/6 ok on
# the live database on 2026-09-01 and again on 2026-09-10, before this landed.
#
# ANY non-zero exit refuses, including 2. The tool's own main() returns 2 for
# "the check could not run", and that alone would argue for failing open the
# way the collapse gate below does — but python also exits 2 when the script
# file is missing, and argparse exits 2 on an unknown flag, so a fail-open arm
# would silently un-wire this gate the day the tool is renamed or its --db flag
# changes. That is not hypothetical: ops/research-liveness.sh passed
# --emit-dq-event, a flag research_liveness.py never implemented, argparse
# exited 2, and the check "had never produced a verdict, on macOS either"
# (REMEDIATION_2026-08-03.md). Refusing costs one day's publication and is
# self-healing; failing open costs the guarantee this block exists to give.
#
# Guarded like the protocol check below it, so an earlier refusal stands. On a
# pass the capture is cleared: a LATER refusal that writes no capture of its
# own (the protocol-document gate) would otherwise publish these six passing
# [ok] lines as its own refusal_stderr. The heartbeat below files this reason
# as --failure, like the liveness refusal, because the grader was not run and
# the remedy is an operator deploy, not a cleared window.
if [ -z "$refusal_reason" ]; then
  "$PY" "$SD/tools/deployment_drift.py" --db "$SD/data/signaldeck.db" \
    > "$STDERR_CAPTURE" 2>&1
  drift_status=$?
  cat "$STDERR_CAPTURE" >> "$LOG"
  if [ "$drift_status" -ne 0 ]; then
    refusal_reason="deployment drift check failed (exit $drift_status) — a mechanism the pre-registration chain claims is not observable in the live database, or the check itself could not run (see the DEPLOYMENT DRIFT lines above in this log); the grader was not run"
  else
    : > "$STDERR_CAPTURE"
  fi
fi

# PROTOCOL-DOCUMENT REGISTRATION — the same fail-closed shape
# require_registered_grader() already has, applied to the protocol DOCUMENT
# instead of the grader. PREREGISTRATION.md §0 makes the chain authoritative over
# the prose and §7 step 1 tells an outside reader to check exactly this digest,
# but nothing enforced it here: the document could describe a protocol no record
# ever froze while this script kept publishing verdicts under it. An unregistered
# protocol document is an unregistered protocol. This can only suppress
# publication — it never edits the document, never re-pins either digest, and
# never alters a verdict.
if [ -z "$refusal_reason" ]; then
  doc_hash=$(sha256_of "$SD/PREREGISTRATION.md")
  chain_hash=$("$PY" - "$SD/data/signaldeck.db" <<'PY'
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
  if [ -z "$chain_hash" ]; then
    refusal_reason="UNREGISTERED PROTOCOL DOCUMENT: the pre-registration chain carries no prereg-document record, so PREREGISTRATION.md (${doc_hash}) freezes nothing and no verdict can be tied to a registered protocol"
  elif [ "$doc_hash" != "$chain_hash" ]; then
    refusal_reason="UNREGISTERED PROTOCOL DOCUMENT: PREREGISTRATION.md hashes ${doc_hash} but the newest prereg-document record pins ${chain_hash}. The protocol document in force is not the registered one. Fix by letting the prereg-registrar append an AMENDMENT record — NOT by re-pinning either digest by hand"
  fi
fi

if [ -z "$refusal_reason" ]; then
  # SURVIVORSHIP BOUND — measured each cycle against SEC EDGAR Form 25 filings,
  # BEFORE the grade, so the bound published beside today's numbers was computed
  # against today's universe instead of being copied forward from the previous
  # JSON. Best-effort by design: it can only ever widen the disclosed bound, so
  # a network failure must not block grading (the grader then reports the bound
  # as stale/absent rather than fabricating one).
  "$PY" "$SD/tools/backfill_delistings.py" --survivorship-bound \
    > "$STDERR_CAPTURE" 2>&1 || \
    echo "survivorship-bound measurement failed (non-fatal); the grade proceeds" \
      >> "$STDERR_CAPTURE"
  cat "$STDERR_CAPTURE" >> "$LOG"

  "$PY" "$SD/tools/accuracy_registry.py" --json "$OUT" > "$STDERR_CAPTURE" 2>&1
  grader_status=$?
  cat "$STDERR_CAPTURE" >> "$LOG"

  # One-sided-book disclosure, merged into the artifact the grader just wrote.
  # It runs AFTER grading and never touches tools/accuracy_registry.py, whose
  # sha256 is pinned in the pre-registration chain -- that grader refuses to run
  # when its own hash changes, and the code deciding verdicts must stay the code
  # the chain froze. This adds a `honesty` block per directional row saying when
  # an accuracy is just a one-sided selection's own base rate; it changes no
  # verdict, no threshold and no retire flag.
  #
  # Its exit code is DELIBERATELY not propagated: a refused row is a disclosure
  # about the model, not a grading outage, and folding it into grader_status
  # would trip the refusal path below and suppress the whole report.
  if [ "$grader_status" -eq 0 ]; then
    "$PY" "$SD/tools/selection_honesty.py" --json "$OUT" --merge >> "$LOG" 2>&1 || true
  fi

  after_generated=$(generated_of "$OUT")
  if [ "$grader_status" -ne 0 ]; then
    refusal_reason="grader exited $grader_status"
  elif [ "$after_generated" = "$before_generated" ]; then
    # Exit 0 with an unmoved `generated` is the same outage wearing a success
    # code: nothing was graded, so nothing may be republished.
    refusal_reason="grader exited 0 but the registry's generated timestamp did not advance (still ${before_generated:-absent})"
  fi

  # PUBLICATION GATE -- the same collapsed-cross-section check /api/accuracy
  # applies before it will serve a single row.
  #
  # Without this the two surfaces gave opposite answers about the SAME window:
  # the HTTP endpoint returned 503 REFUSED with zero rows, while this script
  # published the identical rows into README.md and the eight INCLUDES
  # documents -- and README's freeze notice asserted the block "currently reads
  # GRADING REFUSED" when it in fact carried a full verdict table. Measured on
  # the live registry at the time this landed: 15 collapsed cross-sections out
  # of 35 graded days, several with 5-8 distinct probabilities across 328
  # symbols. Those rows grade one market-wide call repeated per symbol.
  #
  # The gate is SHARED code (internal/api.CollapsedGradingWindow), deliberately
  # not a second implementation in shell or python: the handler's own comment
  # requires it to refuse "on the SAME evidence internal/forecastmon uses, so
  # the publication surface and the monitor cannot disagree".
  #
  # FAILS OPEN, like the handler. Exit 2 (undetermined) publishes -- refusing on
  # a failed read would wedge publication shut on a transient database error
  # rather than on evidence. Only an explicit exit 1, a measured collapse,
  # refuses. cmd/forecastmon is NOT a substitute: it takes a fixed --days
  # lookback, and this gate exists precisely because a fixed window misses a
  # collapse just outside it or refuses forever on one the grader never touched.
  if [ -z "$refusal_reason" ]; then
    collapse_out=$(go run -C "$SD/daemon" ./cmd/collapsecheck       --db "$SD/data/signaldeck.db" --registry "$OUT" 2>>"$LOG")
    collapse_status=$?
    case "$collapse_status" in
      1) refusal_reason="publication gate: $collapse_out" ;;
      0) ;;
      *) echo "collapse gate undetermined (exit $collapse_status) -- publishing, as /api/accuracy does" >> "$LOG" ;;
    esac
  fi
fi

# REFUSAL PATH — the whole point of this branch is that a grading outage must be
# impossible to mistake for a healthy run. It never adds or improves a number:
# it removes the accuracy tables from README.md and puts the refusal in their
# place, restores PREV so transition detection survives, and pages.
if [ -n "$refusal_reason" ]; then
  # Record the refusal where the DAEMON can see it. Until 2026-08-04 a grading
  # outage was visible only in this log and in README prose, so /api/accuracy
  # and every other consumer went on serving the last good numbers with no way
  # to know they were stale. The rule that decided this is above, and stays
  # above: this line only reports the decision.
  case "$refusal_reason" in "grader exited"*|*"liveness check failed"*|"deployment drift"*) hb_mode=--failure ;; *) hb_mode=--refused ;; esac  # a gate refusal is a healthy grader saying no; drift and liveness mean the grader never ran
  "$PY" "$SD/tools/grader_heartbeat.py" "$hb_mode" --error "$refusal_reason" \
    >> "$LOG" 2>&1 || echo "heartbeat write failed (non-fatal)" >> "$LOG"

  if [ -f "$PREV_BACKUP" ]; then
    mv -f "$PREV_BACKUP" "$PREV"
  else
    rm -f "$PREV"
  fi

  refusal_text=$("$PY" - "$SD" "$OUT" "$STDERR_CAPTURE" "$refusal_reason" <<'PY'
import datetime as dt, json, pathlib, re, sys

sd, out_p, err_p, reason = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
now = dt.datetime.now()

try:
    stale = json.load(open(out_p))
except Exception:
    stale = {}
# A registry already in REFUSED state keeps its original refused_since so the
# outage's real age is visible, not reset to zero by each retry.
refused_since = stale.get("refused_since") or now.isoformat(timespec="seconds")
graded_at = stale.get("graded_at") or stale.get("generated")
if stale.get("status") == "REFUSED":
    # Retrying into an already-refused registry: keep the last SUCCESSFUL grade
    # and its rows, rather than nesting refusal inside refusal.
    graded_at = stale.get("graded_at")
    stale = stale.get("stale_last_registry") or {}

age = "unknown"
if graded_at:
    try:
        delta = now - dt.datetime.fromisoformat(graded_at)
        age = f"{delta.total_seconds() / 3600:.1f}h"
    except ValueError:
        pass

stderr_text = ""
try:
    stderr_text = open(err_p).read().strip()[-4000:]
except OSError:
    pass

envelope = {
    "status": "REFUSED",
    "generated": now.isoformat(timespec="seconds"),
    "graded_at": graded_at,
    "refused_since": refused_since,
    "last_successful_grade_age": age,
    "refusal_reason": reason,
    "refusal_stderr": stderr_text,
    # No rows: a refused cycle has no verdicts. The stale registry is kept
    # nested (never at the top level) so nothing is lost and nothing can be
    # mistaken for a fresh grade.
    "rows": [],
    "stale_last_registry": stale or None,
}
with open(out_p, "w") as f:
    json.dump(envelope, f, indent=1)

lines = [
    "## Live accuracy (auto-updated)",
    "",
    "<!-- Generated by ops/accuracy-registry.sh from data/accuracy_registry.json — do not edit by hand. -->",
    "",
    "> **GRADING REFUSED — no accuracy numbers are published.**",
    f"> The grader refused at {now.isoformat(timespec='seconds')} ({reason}). The last "
    f"successful grade was {graded_at or 'never'} ({age} old). The previously published "
    "tables have been REMOVED rather than reprinted, because a number graded by code that "
    "refused to run today is not a live number.",
    "",
    "> The withheld grade stays inside `data/accuracy_registry.json` under `stale_last_registry` for the historical record and is not reprinted here; the grader's full output (which contains figures) is in `logs/accuracy-registry.log`, because a refusal notice that quotes the refused numbers is not a refusal.",
    "",
    "Full grading methodology and per-row JSON: `tools/accuracy_registry.py`, "
    "`data/accuracy_registry.json`; in-app at `/accuracy`.",
]
section = "\n".join(lines)

BEGIN, END = "<!-- LIVE-ACCURACY:BEGIN -->", "<!-- LIVE-ACCURACY:END -->"
readme_path = sd / "README.md"
text = readme_path.read_text()
block = f"{BEGIN}\n{section}\n{END}"
if BEGIN in text and END in text:
    new = re.sub(re.escape(BEGIN) + r".*?" + re.escape(END), lambda _: block, text, flags=re.S)
elif "## Run it" in text:
    new = text.replace("## Run it", block + "\n\n## Run it", 1)
else:
    new = text.rstrip() + "\n\n" + block + "\n"
if new != text:
    readme_path.write_text(new)

print(f"ACCURACY GRADING REFUSED ({reason}). Last successful grade {graded_at or 'never'} "
      f"({age} old). README accuracy tables removed; no numbers published.")
PY
)
  { echo "ACCURACY REGISTRY REFUSED:"; echo "$refusal_text"; } >> "$LOG"

  # THE REFUSAL HAS TO REACH EVERY SURFACE, NOT JUST README.
  #
  # The block above rewrites README.md and then exits. partials/live_accuracy.md
  # and the six documents in partials/INCLUDES.txt carry the SAME grade through a
  # separate generated block, and the regeneration that refreshes them sits far
  # below this exit — so on a refused cycle it never ran. Measured 2026-08-21:
  # the registry had been REFUSED since 2026-08-20T14:06:29 with zero rows and
  # README said "GRADING REFUSED — no accuracy numbers are published", while
  # CASE_STUDY, HOW_PREDICTORS_WORK, INSTITUTIONAL_GAP, PREDICTION_PROCESS,
  # SHIP_READINESS and STRATEGY_DECK each still published a full six-row verdict
  # table stamped 2026-08-19, with nothing on it saying so.
  #
  # That is the divergence cmd/collapsecheck was added to end, surviving in the
  # OTHER direction: the gate fired, and the refusal only reached one document.
  #
  # live_accuracy.py already renders a refused registry correctly — it falls back
  # to stale_last_registry behind a "STALE — this is not a current grade" banner
  # naming the refusal and its age. It simply was never called here. Failures are
  # WARNed rather than fatal: a refused cycle already exits non-zero, and losing
  # the notification below would trade one silent surface for another.
  "$PY" "$SD/tools/live_accuracy.py" --write     || echo "WARN: partials/live_accuracy.md not regenerated on the refusal path" >> "$LOG"
  "$PY" "$SD/tools/live_accuracy.py" --inject $(cat "$SD/partials/INCLUDES.txt")     || echo "WARN: live-accuracy blocks not re-injected on the refusal path" >> "$LOG"

  report_uncommitted_docs
  notify_remote "SignalDeck accuracy registry — $refusal_text"
  sd_notify "SignalDeck accuracy" "Grading REFUSED — README accuracy tables removed. See the log."
  exit 0  # a recorded refusal is this job succeeding at its job; exit 1 made the task red for the whole window (F17)
fi

rm -f "$PREV_BACKUP"

# The grade is real: the grader exited 0 AND the registry's timestamp advanced,
# both checked above. Record it so the daemon can tell a fresh grade from a
# stale one — /api/accuracy refuses to publish without a recent success here.
"$PY" "$SD/tools/grader_heartbeat.py" --success \
  >> "$LOG" 2>&1 || echo "heartbeat write failed (non-fatal)" >> "$LOG"

# Regenerate the "Live accuracy (auto-updated)" section of README.md from the
# fresh registry, between the LIVE-ACCURACY markers. The point: a FAILED verdict
# must be one click from the headline, not buried in a log only this Mac reads.
# FAILED rows sort first, every directional row carries its verdict verbatim,
# day-clustered CI, driving baseline and effective n, and the flagship
# retirement is stated as a permanent fact even though the retired model no
# longer appears in the post-epoch registry rows.
"$PY" - "$SD" <<'PY' >> "$LOG" 2>&1
import json, pathlib, re, sys

sd = pathlib.Path(sys.argv[1])
reg = json.load(open(sd / "data" / "accuracy_registry.json"))
readme_path = sd / "README.md"

def verdict_of(r):
    """The row's verdict, or WITHHELD when the grader dropped the field.

    Provenance-unattributable rows have no "verdict" key at all — the grader
    drops it rather than downgrading it, because an absent verdict cannot be
    quoted as one. Reading it directly raised KeyError here and killed the
    entire publishing run, so a deliberate withholding upstream turned into a
    total publication outage downstream.
    """
    return r.get("verdict") or "WITHHELD (provenance unresolvable)"


def pct(x, dec=1):
    return "—" if x is None else f"{x * 100:.{dec}f}%"

def baseline(r):
    vals = [v for v in (r.get("null_hindsight"), r.get("null_prequential")) if v is not None]
    if vals:
        return max(vals)  # null_policy: the stricter (higher) null drives the verdict
    return r.get("null_acc")

def ci_cell(r):
    c = r.get("ci")
    if c:
        return f"[{pct(c[0])}, {pct(c[1])}]"
    return "withheld" if r.get("ci_method") == "withheld" else "—"

def eff_n_cell(r):
    e = r.get("effective_n")
    if e is not None:
        return f"{e:,.0f}"
    n = r.get("live_n") or 0
    return f"{n:,} raw" if n else "0"

def skill_cell(r):
    s = r.get("skill")
    return "—" if s is None else f"{s * 100:+.1f}pp"

rows = reg.get("rows", [])
directional = sorted(
    (r for r in rows if r.get("family") == "direction"),
    key=lambda r: 0 if verdict_of(r).startswith("FAILED") else 1,
)
structural = [r for r in rows if r.get("family") == "structure"]

lines = [
    "## Live accuracy (auto-updated)",
    "",
    "<!-- Generated by ops/accuracy-registry.sh from data/accuracy_registry.json — do not edit by hand. -->",
    "",
    f"_Regenerated {reg.get('generated', '?')} · verdicts come from day-clustered intervals, "
    f"never point estimates · survivorship epoch {reg.get('survivorship_epoch', '?')} "
    "(earlier rows were graded against a survivor-seeded universe and are excluded)._",
    "",
    # This preamble used to hardcode the pre-retirement figures (1d 48.1% vs
    # 54.6% over 13,058, and two more) and assert that "the entire day-clustered
    # CI" sat below the baseline for every row. Both were wrong to print here.
    # The figures were a superseded PRE-EPOCH population, restated inside a
    # block whose whole purpose is to be generated from the current registry —
    # a hand-typed live record sitting on top of the generated one, which is the
    # exact contradiction partials/live_accuracy.md exists to end. And the
    # interval claim covered rows whose intervals are WITHHELD for insufficient
    # distinct days, so it asserted evidence the grader refuses to publish.
    # The retirement is a fact and stays; the numbers behind it belong to the
    # generated table below and to the reconciliation, not to a static string.
    "**The flagship directional ensemble is RETIRED.** It no longer emits; the rows below",
    "are its post-retirement shadow record, restarted at the survivorship epoch. The",
    "pre-retirement figures behind that decision are a superseded pre-epoch population and",
    "are deliberately not restated here — see `proofs/P2_LIVE_RECORD_RECONCILIATION.md`.",
    "",
    "| Predictor | Verdict | Live acc | Skill vs baseline | Baseline (stricter null) | 95% CI (day-clustered) | Effective n |",
    "|---|---|---|---|---|---|---|",
]
for r in directional:
    v = verdict_of(r)
    verdict_md = f"**{v}**" if v.startswith("FAILED") else v
    lines.append(
        f"| {r['predictor']} | {verdict_md} | {pct(r.get('live_acc'))} | {skill_cell(r)} "
        f"| {pct(baseline(r))} | {ci_cell(r)} | {eff_n_cell(r)} |"
    )

if structural:
    pending = [r for r in structural if verdict_of(r).startswith("PENDING")]
    lines += [
        "",
        f"**Structural claims (trend/vol/liquidity): {len(pending)}/{len(structural)} PENDING — "
        "backtested numbers, not live records yet.**",
        "",
        "| Predictor | Backtest claim | Status |",
        "|---|---|---|",
    ]
    for r in structural:
        lines.append(f"| {r['predictor']} | {pct(r.get('claimed'))} | {verdict_of(r)} |")

lines += [
    "",
    "Full grading methodology and per-row JSON: `tools/accuracy_registry.py`, "
    "`data/accuracy_registry.json`; in-app at `/accuracy`.",
]
section = "\n".join(lines)

BEGIN, END = "<!-- LIVE-ACCURACY:BEGIN -->", "<!-- LIVE-ACCURACY:END -->"
text = readme_path.read_text()
block = f"{BEGIN}\n{section}\n{END}"
if BEGIN in text and END in text:
    new = re.sub(re.escape(BEGIN) + r".*?" + re.escape(END), lambda _: block, text, flags=re.S)
elif "## Run it" in text:
    new = text.replace("## Run it", block + "\n\n## Run it", 1)
else:
    new = text.rstrip() + "\n\n" + block + "\n"
if new != text:
    readme_path.write_text(new)
    print("README.md live-accuracy section regenerated")
PY

# README is NOT the only surface carrying this grade. partials/live_accuracy.md
# and every document in partials/INCLUDES.txt embed the same record, and §8 of
# STRATEGY_DECK.md embeds figures measured from the same database. Regenerating
# README alone left all of them a grade behind, so the three gates that police
# exactly that — live_accuracy --check, deck_facts --check and docs_gate — went
# RED at 14:05 every day and stayed red until a human ran these commands by hand.
# Observed twice on 2026-08-09: once as the overnight state, and again the moment
# this job re-graded. A daily job that predictably breaks the publish gate is the
# gate's problem, not the operator's, so the same run now refreshes every surface.
# A regeneration failure is tracked, not just echoed. Warning into a log that
# nothing reads is how a stale published number survives: check-grader-health.ps1
# only watches WAL size, and the two gates that police exactly this drift --
# live_accuracy --check and deck_facts --check -- sit behind an `exit 0` guard in
# ci.yml (lines 197-211) for the gitignored data/, so on a runner they never
# execute at all. That left the WARNs below with no reader anywhere.
docs_stale=0
"$PY" "$SD/tools/live_accuracy.py" --write \
  || { echo "WARN: partials/live_accuracy.md not regenerated"; docs_stale=1; }
"$PY" "$SD/tools/live_accuracy.py" --inject $(cat "$SD/partials/INCLUDES.txt") \
  || { echo "WARN: live-accuracy blocks not re-injected"; docs_stale=1; }
# deck_facts reads the 4.9 GB database; a failure here is not fatal to grading.
"$PY" "$SD/tools/deck_facts.py" --inject "$SD/STRATEGY_DECK.md" \
  || { echo "WARN: STRATEGY_DECK.md §8 not re-injected"; docs_stale=1; }

# H9: page on VERDICT TRANSITIONS — a predictor changing state (PENDING→FAILED,
# NO SKILL→SUPPORTED, …) is the page-worthy event; an unchanged state is not.
# Verdicts are normalized to their leading class so a PENDING first-grade date
# rolling forward stays quiet.
transitions=$("$PY" - "$PREV" "$OUT" <<'PY'
import json, sys

def load(p):
    try:
        with open(p) as f:
            return json.load(f)
    except Exception:
        return None

def norm(v):
    for sep in (" —", " (", " -"):
        i = v.find(sep)
        if i > 0:
            v = v[:i]
    return v.strip()

prev, cur = load(sys.argv[1]), load(sys.argv[2])
if not prev or not cur:
    sys.exit(0)
pv = {(r["predictor"], r.get("family", ""), r.get("band", "")): norm(r.get("verdict") or "WITHHELD")
      for r in prev.get("rows", [])}
lines = []
for r in cur.get("rows", []):
    k = (r["predictor"], r.get("family", ""), r.get("band", ""))
    new = norm(r.get("verdict") or "WITHHELD")
    old = pv.get(k)
    if old is not None and old != new:
        lines.append(f"{r['predictor']} [{r.get('band','all')}]: {old} -> {new}")
print("\n".join(lines))
PY
)
if [ -n "$transitions" ]; then
  { echo "VERDICT TRANSITIONS:"; echo "$transitions"; } >> "$LOG"
  notify_remote "SignalDeck accuracy registry — verdict transition(s):
$transitions"
  sd_notify "SignalDeck accuracy" "Verdict transition in the accuracy registry — see the log."
fi

# Alert only on a predictor that is actively contradicted by its own live record.
# PENDING is normal and must stay quiet, or the alarm stops meaning anything.
if grep -q "^ACTION REQUIRED" "$LOG" 2>/dev/null && \
   tail -60 "$LOG" | grep -q "^ACTION REQUIRED"; then
  n=$("$PY" -c "
import json
try:
    d=json.load(open('$OUT'))
    print(sum(1 for r in d['rows'] if (r.get('verdict') or '').startswith('FAILED')))
except Exception:
    print(0)
")
  if [ "${n:-0}" -gt 0 ]; then
    sd_notify "SignalDeck accuracy" "$n predictor(s) contradicted by their own live record — see the accuracy registry."
  fi
fi

# EXPLICIT EXIT. Everything above that can invalidate a grade already exits 1 on
# its own (the REFUSAL PATH, which covers both a non-zero grader and a grader
# that exited 0 without advancing the registry's timestamp). What was missing is
# only that the script ENDED on the notification `if` above, so its status was
# whatever that branch happened to leave behind -- incidental, not a statement.
# Say it deliberately instead: reaching here means the grade was real and
# published.
#
# The two liveness probes (research, anchor) stay ADVISORY on purpose and are
# not folded in: they measure freshness, not correctness. Unpublished anchors or
# a stale research loop do not make a graded number wrong, and failing this task
# for them would train the operator to ignore a red accuracy job.
#
# A failed document regeneration IS folded in, because it is not that kind of
# event. It does not mean a surface is a little behind; it means the documents a
# reader actually sees no longer carry the grade this run just computed, while
# the run reports success. That is a published number being wrong, which is the
# same class as the REFUSAL PATH above -- and unlike the liveness probes, nothing
# downstream can catch it (see the docs_stale comment where it is set).
report_uncommitted_docs

if [ "${docs_stale:-0}" != "0" ]; then
  echo "FAILED: the grade was computed and published, but at least one document" \
       "surface was NOT regenerated from it (see the WARN line above). The" \
       "published docs are now STALE relative to this grade. Re-run the failing" \
       "generator before quoting any number from them."
  exit 1
fi
exit 0
