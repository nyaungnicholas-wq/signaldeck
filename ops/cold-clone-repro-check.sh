#!/bin/bash
# Cold-clone reproduce-divergence check.
#
# REPRODUCE.md promises that every published number can be regenerated from this
# repository alone. That promise was prose, and while it was prose the state was
# inverted in the dangerous direction: a stranger cloning HEAD got a full verdict
# table out of an older grader, while the operator's own working-tree grader
# refused to grade at all because the registration gate was not satisfied. More
# published numbers from weaker code, with the safety gate silently absent.
#
# This turns the promise into an invariant a build can fail. It exports HEAD with
# `git archive` — precisely what a fresh clone contains, index and worktree both
# invisible to it — and grades the reproducibility snapshot in BOTH trees. There
# is exactly ONE passing terminal state:
#
#   * both grade (exit 0), and then agree on every frozen field.
#
# One grading and one refusing is the divergence, in either direction. A cold
# clone that publishes MORE than the operator's tree means the gate is missing
# downstream; a cold clone that publishes LESS means a published number cannot be
# regenerated from the repository. Both are failures here.
#
# Both refusing IDENTICALLY is also a failure, and used not to be. That branch
# printed "COLD-CLONE OK — a fresh clone of HEAD reproduces the working tree's
# published state exactly", which is false whenever it fires: while both trees
# refuse, neither reproduces any published state at all. It made this gate blind
# to the one failure it is named for. The shipped snapshot spent three weeks
# pinning a grader nine re-registrations stale — the reproduce path dead the
# whole time — and this script reported OK on every run. Agreement is not
# reproduction: the product keeps publishing numbers either way, so a snapshot
# that grades nothing is a broken promise, not a consensus.
#
# One-directional: it can only fail. It never edits, commits, or publishes, and
# it can never change a number — it compares two runs of an unmodified grader.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

# `python3` is a Microsoft Store alias STUB under Git Bash: it resolves on PATH,
# prints "Python was not found", and exits non-zero. Both grader invocations
# below used it directly, so this check could never pass on Windows — it died at
# the first run_grade with exit 49 while reporting nothing about reproducibility.
# The other ops scripts already resolve an interpreter that actually runs;
# sd_py is that resolver, and it belongs here for the same reason.
# shellcheck source=lib-portable.sh
. "$(dirname "$0")/lib-portable.sh"
PY="$(sd_py)"
if [ -z "$PY" ]; then
  echo "  ✗ no working python on PATH (tried python3, python, py) — cannot grade" >&2
  exit 2
fi

SNAP_REQUIRED=(repro/grading_protocol.csv repro/MANIFEST.json)

work=$(mktemp -d "${TMPDIR:-/tmp}/sd-coldclone.XXXXXX") || exit 2
trap 'rm -rf "$work"' EXIT
cold="$work/cold"
mkdir -p "$cold" || exit 2

echo "── exporting HEAD ($(git rev-parse --short HEAD)) ──────────────────────"
if ! git archive HEAD | tar -x -C "$cold"; then
  echo "  ✗ git archive HEAD failed — cannot construct a cold clone"
  exit 1
fi

fail=0
echo "── the archive must carry the grading record and the snapshot manifest ─"
for p in "${SNAP_REQUIRED[@]}"; do
  if [ -f "$cold/$p" ]; then
    printf '  ✓ %s\n' "$p"
  else
    printf '  ✗ %s — absent from a clone of HEAD; the grader it registers is unpinnable\n' "$p"
    fail=1
  fi
done
[ "$fail" = "0" ] || { echo ""; echo "COLD-CLONE CHECK FAILED"; exit 1; }

# Grade the same snapshot in both trees. Each run writes its registry JSON into
# the scratch dir, never into the tree, so neither run can perturb the other's
# look counter (accuracy_registry.py maxes looks over any registry it finds at
# --json) and neither leaves an artifact behind.
run_grade() {
  local tree="$1" tag="$2"
  ( cd "$tree" && "$PY" tools/accuracy_registry.py --snapshot repro \
      --json "$work/$tag.json" ) >"$work/$tag.out" 2>"$work/$tag.err"
  echo "$?" >"$work/$tag.status"
}

echo "── grading repro/ in the cold clone and in the worktree ────────────────"
run_grade "$cold" cold
run_grade "." worktree

"$PY" - "$work" <<'PY'
import json, os, re, sys

work = sys.argv[1]

# Frozen fields: the ones a reader quotes. Anything here differing between a
# cold clone and the worktree means a published number is not regenerable from
# the repository alone.
FROZEN = ["max_alpha", "family_size", "looks", "divisor", "corrected_alpha",
          "ci_z", "multiplicity_rule", "min_independent_n", "min_distinct_blocks",
          "survivorship_epoch", "null_policy", "auto_retire_rule",
          "grader_sha256", "grading_protocol_seq", "revision_epoch"]
ROW_FROZEN = ["predictor", "family", "claimed", "live_n", "live_acc", "ci",
              "verdict", "claim_verdict"]


def refusal_class(status, err, out):
    """A coarse, message-independent class for a refusal.

    Two trees must refuse for the SAME reason, but rewording an abort message
    must not fail the build — the class is the leading clause of the refusal
    with paths, digests and digits stripped, plus the exit status.
    """
    text = (err.strip() or out.strip()).splitlines()
    line = text[-1] if text else ""
    line = re.split(r"[:—]", line)[0]
    line = re.sub(r"[0-9a-f]{8,}", "<hash>", line)
    line = re.sub(r"[0-9]+", "<n>", line)
    line = re.sub(r"\S*/\S*", "<path>", line)
    return f"exit={status} {' '.join(line.split()).lower()[:80]}"


def read(tag):
    status = int(open(os.path.join(work, tag + ".status")).read().strip())
    err = open(os.path.join(work, tag + ".err")).read()
    out = open(os.path.join(work, tag + ".out")).read()
    payload = None
    p = os.path.join(work, tag + ".json")
    if status == 0 and os.path.exists(p):
        try:
            payload = json.load(open(p))
        except (OSError, json.JSONDecodeError):
            payload = None
    return status, err, out, payload


cs, cerr, cout, cpay = read("cold")
ws, werr, wout, wpay = read("worktree")

cold_graded = cs == 0
work_graded = ws == 0
bad = []

if cold_graded != work_graded:
    who = "the cold clone" if cold_graded else "the worktree"
    other = "the worktree" if cold_graded else "the cold clone"
    bad.append(
        f"DIVERGENCE: {who} produced a verdict table while {other} refused.\n"
        f"    cold clone : exit {cs} {(cerr.strip().splitlines() or [''])[-1]}\n"
        f"    worktree   : exit {ws} {(werr.strip().splitlines() or [''])[-1]}\n"
        "    A published number must be regenerable from this repository alone,\n"
        "    and a refusal must be too. One of these two states is not shipped.")
elif not cold_graded:
    cc, wc = refusal_class(cs, cerr, cout), refusal_class(ws, werr, wout)
    if cc != wc:
        bad.append("DIVERGENT REFUSAL CLASS: both trees refused, for different reasons.\n"
                   f"    cold clone : {cc}\n"
                   f"    worktree   : {wc}")
    else:
        bad.append(
            f"DEAD REPRODUCE PATH: both trees refuse identically [{cc}].\n"
            "    Agreement is not reproduction. REPRODUCE.md promises every published\n"
            "    number can be regenerated from this repository alone; while both trees\n"
            "    refuse, no reader can regenerate any of them — and the product goes on\n"
            "    publishing those numbers regardless.\n"
            f"    cold clone : exit {cs} {(cerr.strip().splitlines() or [''])[-1]}\n"
            f"    worktree   : exit {ws} {(werr.strip().splitlines() or [''])[-1]}\n"
            "    Re-cut the snapshot:  python3 tools/make_repro_snapshot.py")
else:
    if cpay is None or wpay is None:
        bad.append("both trees graded but a registry JSON was missing or unparseable")
    else:
        for k in FROZEN:
            if cpay.get(k) != wpay.get(k):
                bad.append(f"frozen field '{k}' differs: cold={cpay.get(k)!r} "
                           f"worktree={wpay.get(k)!r}")
        crows = {r.get("predictor"): r for r in cpay.get("rows", [])}
        wrows = {r.get("predictor"): r for r in wpay.get("rows", [])}
        for name in sorted(set(crows) ^ set(wrows)):
            where = "only the cold clone" if name in crows else "only the worktree"
            bad.append(f"row '{name}' is published by {where}")
        for name in sorted(set(crows) & set(wrows)):
            for k in ROW_FROZEN:
                if crows[name].get(k) != wrows[name].get(k):
                    bad.append(f"row '{name}' field '{k}' differs: "
                               f"cold={crows[name].get(k)!r} "
                               f"worktree={wrows[name].get(k)!r}")
        if not bad:
            print(f"  ✓ both trees grade and agree on every frozen field "
                  f"({len(crows)} rows)")

print("")
if bad:
    for b in bad:
        print("  ✗ " + b)
    print("")
    print("COLD-CLONE CHECK FAILED — HEAD and the working tree do not reproduce "
          "the same published state.")
    sys.exit(1)
print("COLD-CLONE OK — a fresh clone of HEAD reproduces the working tree's "
      "published state exactly.")
PY
exit $?
