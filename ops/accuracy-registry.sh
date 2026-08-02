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

  after_generated=$(generated_of "$OUT")
  if [ "$grader_status" -ne 0 ]; then
    refusal_reason="grader exited $grader_status"
  elif [ "$after_generated" = "$before_generated" ]; then
    # Exit 0 with an unmoved `generated` is the same outage wearing a success
    # code: nothing was graded, so nothing may be republished.
    refusal_reason="grader exited 0 but the registry's generated timestamp did not advance (still ${before_generated:-absent})"
  fi
fi

# REFUSAL PATH — the whole point of this branch is that a grading outage must be
# impossible to mistake for a healthy run. It never adds or improves a number:
# it removes the accuracy tables from README.md and puts the refusal in their
# place, restores PREV so transition detection survives, and pages.
if [ -n "$refusal_reason" ]; then
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
    "```",
    stderr_text or "(no grader output captured)",
    "```",
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
  notify_remote "SignalDeck accuracy registry — $refusal_text"
  osascript -e "display notification \"Grading REFUSED — README accuracy tables removed. See the log.\" with title \"SignalDeck accuracy\"" >/dev/null 2>&1
  exit 1
fi

rm -f "$PREV_BACKUP"

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
    key=lambda r: 0 if r["verdict"].startswith("FAILED") else 1,
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
    "**The flagship directional ensemble is RETIRED (2026-07-24).** On its full live record",
    "every directional row graded **FAILED** — the entire day-clustered CI below the",
    "majority-class baseline: 1d 48.1% vs 54.6% over 13,058 independent obs (skill **−6.5pp**),",
    "1w 46.2% vs 54.4% over 9,164 (skill **−8.2pp**), 1d high-conviction 48.6% vs 56.2% over",
    "8,272 (skill **−7.6pp**). It no longer emits; the rows below are its post-retirement",
    "shadow record, restarted at the survivorship epoch.",
    "",
    "| Predictor | Verdict | Live acc | Skill vs baseline | Baseline (stricter null) | 95% CI (day-clustered) | Effective n |",
    "|---|---|---|---|---|---|---|",
]
for r in directional:
    v = r["verdict"]
    verdict_md = f"**{v}**" if v.startswith("FAILED") else v
    lines.append(
        f"| {r['predictor']} | {verdict_md} | {pct(r.get('live_acc'))} | {skill_cell(r)} "
        f"| {pct(baseline(r))} | {ci_cell(r)} | {eff_n_cell(r)} |"
    )

if structural:
    pending = [r for r in structural if r["verdict"].startswith("PENDING")]
    lines += [
        "",
        f"**Structural claims (trend/vol/liquidity): {len(pending)}/{len(structural)} PENDING — "
        "backtested numbers, not live records yet.**",
        "",
        "| Predictor | Backtest claim | Status |",
        "|---|---|---|",
    ]
    for r in structural:
        lines.append(f"| {r['predictor']} | {pct(r.get('claimed'))} | {r['verdict']} |")

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
pv = {(r["predictor"], r.get("family", ""), r.get("band", "")): norm(r["verdict"])
      for r in prev.get("rows", [])}
lines = []
for r in cur.get("rows", []):
    k = (r["predictor"], r.get("family", ""), r.get("band", ""))
    new = norm(r["verdict"])
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
  osascript -e "display notification \"Verdict transition in the accuracy registry — see the log.\" with title \"SignalDeck accuracy\"" >/dev/null 2>&1
fi

# Alert only on a predictor that is actively contradicted by its own live record.
# PENDING is normal and must stay quiet, or the alarm stops meaning anything.
if grep -q "^ACTION REQUIRED" "$LOG" 2>/dev/null && \
   tail -60 "$LOG" | grep -q "^ACTION REQUIRED"; then
  n=$("$PY" -c "
import json
try:
    d=json.load(open('$OUT'))
    print(sum(1 for r in d['rows'] if r['verdict'].startswith('FAILED')))
except Exception:
    print(0)
")
  if [ "${n:-0}" -gt 0 ]; then
    osascript -e "display notification \"$n predictor(s) contradicted by their own live record — see the accuracy registry.\" with title \"SignalDeck accuracy\"" >/dev/null 2>&1
  fi
fi
