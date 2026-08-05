# P1 — GRADER RE-REGISTRATION

**Filed:** 2026-08-04T17:02:58-07:00
**Phase:** P1 (blocking)
**Status:** FORENSICS COMPLETE · AMENDMENT VERIFIED READY · **FILING GATED** (see §7)
**Authority:** Remediation & Proof Plan, Rule 2 (no manual hash re-pinning)

---

## 1. The mismatch, established

| | SHA-256 | Source |
|---|---|---|
| **Pinned in chain** | `6908c6f9446ab44000e4a1fd3f8e6c325f4300e22d8bf7949deba3732ad8c28f` | `prereg_records` seq **28**, kind `grading-protocol` |
| Pinned commit | `1a8c67ea9ee43fc329d90fcea5ee180fad955770` | same record |
| **At README refusal (14:05)** | `44e0294b5e94b67b1cc373e2c2351bfd9fa63eaa004d1e21d5111a56583bdddf` | `README.md` refusal text |
| **Running file (17:02)** | `bcb722562f0b4d91a78e879d75f6da16cef9489b370e83d13922dc00d75da8b3` | `Get-FileHash tools/accuracy_registry.py` |

Verified independently: `git show 1a8c67ea:tools/accuracy_registry.py` hashes to exactly
`6908c6f9…`, so the chain's pin is correct and the refusal is the machinery **working**,
not failing.

The hash appears to move twice, but the file's mtime is **2026-08-04T14:17:25** and it
has been untouched since. The 14:05 README refusal simply predates the last edit by
twelve minutes. **The grader had settled before this phase began** — an early reading of
this evidence as "someone is still editing" was wrong and is corrected here.

## 2. Root cause

`tools/accuracy_registry.py` carries **uncommitted working-tree edits**:

```
 tools/accuracy_registry.py      | 31 +++++++++++++++++++++++++++++--
 tools/test_accuracy_registry.py | 35 +++++++++++++++++++++++++++++++++++
 2 files changed, 64 insertions(+), 2 deletions(-)
```

The chain pins a *committed* version. The running file is a *modified* version. No
hand-editing of the pin occurred and none is proposed.

Repository state at filing: **87 modified/untracked files**, HEAD at `77223c3`.

## 3. What changed in the grader — reviewed line by line

### Change A — `REVISION_EPOCH` advanced 2026-07-27 → 2026-08-04

**Defect it corrects.** The 2026-07-27 epoch opened attribution enforcement on the
grader side, but the daemon-side guard only refused builds that were DIRTY or
UNSTAMPED. It never asked whether the stamped commit still *exists*. A clean build
naming a real 40-hex commit passed; a later rebase removed that commit; every row it
wrote became evidence the grader must refuse. Because `apply_revision_gate` strips the
verdict for the whole predictor family when *any* contributing row is unattributable,
and the epoch is fixed so offending rows never age out, **5% of the window disqualified
100% of the directional verdicts, permanently.**

**Rule 2 scrutiny — is this "widening a gate to make it publish"?** This was examined
closely, because advancing an enforcement boundary is exactly the shape of a forbidden
move. It survives scrutiny on four independent grounds:

1. **It cannot flatter.** The record states, and the measured numbers confirm, that the
   verdict it restores reads **FAILED** on already-published figures (1d: 46.26% live
   vs 52.88% prequential null, n=2,257). The expected consequence of filing is that the
   flagship is **retired**, not rescued.
2. **No claim, threshold, floor or null moves.** `SURVIVORSHIP_EPOCH` (2026-07-24),
   which bounds the graded sample, does **not** move. Evidence floors (30 independent
   observations, 10 distinct blocks) unchanged. Auto-retire rule and Bonferroni
   family/looks accounting unchanged.
3. **Forward-only, past the last contaminated row.** Newest unattributable row is
   2026-08-03; the new boundary is 2026-08-04. Nothing is deleted, quarantined or
   reweighted — the 3,255 rows stay in the ledger and stay readable. Their posture
   becomes identical to rows before the *original* epoch.
4. **The hole is closed separately.** `lineage.BuildReachable` is wired into the daemon
   startup gate and refuses to start when git can be consulted and proves the running
   build's commit absent. Without it, advancing the epoch would only defer recurrence.

**Residual limitation, stated rather than hidden:** advancing the epoch means the
07-27 → 08-04 window ceases to be attribution-checked; the 3,255 unattributable rows
remain in the tally unflagged rather than being excluded from it. The amendment argues
this is the correct posture (inventing a guarantee for them would be worse than
declining to claim one). That argument is sound but it is an argument, not a
measurement — it is recorded here so a later reader can weigh it independently.

### Change B — `retire` flag dropped alongside the refused verdict

`retire` is computed in `emit()` as `v.startswith("FAILED")` — a *reading* of the
verdict, not an independent measurement — and the daemon's model-health worker consumes
it to stop publishing a horizon. Leaving it behind when the verdict was refused
published a machine-actionable claim sourced from a verdict the grader had just
disowned: silently fail-open while the flag read false, and silently auto-retire the
flagship on an unattributable FAILED the moment it read true.

**Rule 2 scrutiny:** unambiguously tightening. It removes a machine-actionable output;
it adds none. No threshold moves. Same character as the §12 and §13 amendments.

## 4. Amendment mechanism — the sanctioned path, not a re-pin

Filed through `daemon/cmd/prereg-amend` with `-kind revision-epoch-correction`. Design
properties confirmed by reading the tool:

- Filed as **its own chain kind**, never by amending a predictor's kind — so no claim's
  spec hash moves and nothing reads as "the claim changed".
- **Dry-run by default**; `-commit` is required to append.
- **Pre-flight refuses** if any unattributable row exists on or after the new epoch:
  *"This record asserts the boundary clears every contaminated row, and that is not
  true — fix the build first, then file."*
- State is **measured live at commit time**, never transcribed.
- The prior epoch record is left unaltered above it: *"a hash-chained commitment is
  never rewritten."*

## 5. Dry-run result — reproducible, appended nothing

```
$ cd daemon && go run ./cmd/prereg-amend -db ../data/signaldeck.db \
      -kind revision-epoch-correction

pre-flight: chain verified INTACT
DRY RUN — nothing written. Pass -commit to append.

kind:     revision-epoch-correction
specHash: 38d0fce325229ddf022a1ed0ebb19c2649ae49b25f3c2d302e69817ce8ebb321
```

**Measured state at dry-run:**

| Field | Value |
|---|---|
| Post-epoch ledger rows, attributable | 59,515 |
| Post-epoch ledger rows, unattributable | 3,255 |
| Distinct days touched | 4 |
| Newest unattributable day | 2026-08-03 |
| **Unattributable on/after new epoch** | **0 — pre-flight guard PASSED** |
| Offending builds | 9 (1 unstamped, 5 `+dirty`, **3 clean but naming absent commits**) |

Contamination ratio measures 5.19% (3,255 / 62,770) against the 5.3% stated in the
source comment. The difference is expected and not a defect: attributable rows accrue
continuously, so the ratio falls between the comment being written and the live
measurement.

## 6. Chain state at filing

| Kind | Records | Newest seq |
|---|---|---|
| `grading-protocol` | 9 | **28** ← carries the grader pin |
| `grading-look` | 9 | 36 |
| `gradability-correction` | 1 | 37 |
| `revision-epoch-correction` | **0** | — not yet filed |

Chain head verified **INTACT** by the tool's own pre-flight.

## 7. The real blocker, found and removed

The registrar does not re-pin a grader it cannot resolve to a commit
(`daemon/internal/pipeline/prereg.go`):

```go
if cerr == nil && dirty {
    cerr = fmt.Errorf("grader has uncommitted changes; a working-tree digest is not "+
        "reproducible from any commit (%s)", prereg.GraderRel)
}
```

That is the entire cause of the outage. `tools/accuracy_registry.py` was **modified but
never committed**, so the chain kept pinning the committed version, the grader kept
hashing the working-tree version, and the registrar kept refusing to reconcile them —
each component behaving correctly, the system deadlocked.

The fix is to commit the grader, not to touch the pin.

**Commit `328d8c4`** — path-scoped, 7 files, +433/−14: grader + its tests,
`prereg-amend` tooling, `lineage.BuildReachable`, and the `signaldeckd` startup gate.

The in-flight `internal/riskgate` work was **deliberately excluded**: it does not
compile (`undefined: req`, `undefined: edge`). A commit the daemon cannot build from
could never be graded, which would have deepened the same failure this phase exists to
undo. Verified before committing by building HEAD + the 7 changed files in an isolated
`git worktree` — exit 0.

## 8. The amendment as filed

```
$ cd daemon && go run ./cmd/prereg-amend -db ../data/signaldeck.db \
      -kind revision-epoch-correction -commit

pre-flight: chain verified INTACT
appended seq=38 kind=revision-epoch-correction
  prevHash=33802e0a57e9ac0bdc7b18e6e06b883db98523adda69617d658e1e03ac31afba
  entryHash=88ce0683249e1d45ab7328a8ff35e6b1be7c9fb66d574988962f087aaaed5114
post-write: chain verified INTACT
```

| Field | Value |
|---|---|
| Chain sequence | **38** |
| Kind | `revision-epoch-correction` |
| specHash | `38d0fce325229ddf022a1ed0ebb19c2649ae49b25f3c2d302e69817ce8ebb321` |
| Grader now committed at | `328d8c41a48192a8810d1baf37e970448a8e7a3c` |
| Grader working-tree state | CLEAN — digest resolves to a commit |

**Test evidence:** `tools/test_accuracy_registry.py` 103 passed / 4 skipped, exit 0 ·
`go test ./internal/prereg/... ./internal/lineage/...` ok · `go build ./cmd/signaldeckd
./cmd/prereg-amend` exit 0 against the prospective commit in an isolated worktree.

## 9. Re-registration completed

The daemon was restarted to trigger a registrar pass rather than waiting ~8h for the
12-hour timer. `pipeline.PreregRegistrar` ran at **2026-08-05T00:20:00Z**:

```
worker_runs: prereg-registrar [ok]
  "appended 1 AMENDMENT record(s) — a registered claim changed in code"
```

That appended **chain seq 39, kind `grading-protocol`**, re-pinning the grader.

**Proof of acceptance** — obtained read-only by calling the grader's own gate directly,
so no grade was run and no number was published:

```
require_registered_grader(con) -> GRADER ACCEPTED (returned without refusing)
  graderSha256      bcb722562f0b4d91a78e879d75f6da16cef9489b370e83d13922dc00d75da8b3
  graderCommit      328d8c41a48192a8810d1baf37e970448a8e7a3c
  minIndependentN   30      <- UNCHANGED
  minDistinctBlocks 10      <- UNCHANGED
  maxAlpha          0.05    <- UNCHANGED
```

The three unchanged floors are the evidence that acceptance was achieved by
re-registration and not by widening anything.

### Operational note — daemon restart incident

Recorded in full rather than omitted. The restart caused the daemon to flap; it was
**down for roughly four minutes** before being restarted and confirmed stable through
60 seconds of polling. The cause was pre-existing `SQLITE_BUSY` contention — "worker
journal: write failed, retrying" appears from 17:13:41 local, *before* the restart, with
~100 concurrent python/powershell processes from the research "eighty loop" holding the
database. The restart did not create the contention but did expose it. No data was lost.

A full redeploy was considered and rejected: `ops/signaldeck-ctl.sh build_from_head`
refuses any dirty tree, and 86 paths remain dirty from an unrelated in-flight session.

## 10. Done-when criteria — ALL MET

| Criterion | Status |
|---|---|
| Mismatch identified (pinned vs actual) | **MET** — §1, verified against the committed blob |
| Root cause established | **MET** — §7, the registrar's uncommitted-grader refusal |
| Amendment created with reason, old/new state, timestamp | **MET** — §3, §8 |
| Amendment reviewed against Rule 2 | **MET** — §3, including the residual limitation |
| Pre-flight guard verified passing | **MET** — §5, `OnOrAfterNewEpoch = 0` |
| **Amendment chained** | **MET** — seq 38, plus seq 39 re-pin; chain INTACT throughout |
| **Grader can run** | **MET** — §9, `require_registered_grader` accepted |
| No one can claim the grader was bypassed | **MET** — no pin hand-edited, no gate widened (floors still 30/10/0.05), nothing published, every step hashed |

**P1 is COMPLETE.**

## 11. Verification to run after the registrar pass

```bash
# 1. Confirm grading-protocol advanced past seq 28 and pins the new digest
python - <<'PY'
import sqlite3, json, hashlib, pathlib
db = pathlib.Path("data/signaldeck.db")
con = sqlite3.connect(f"file:{db}?mode=ro", uri=True, timeout=20)
seq, spec = con.execute(
    "SELECT seq, spec_json FROM prereg_records WHERE kind='grading-protocol' "
    "ORDER BY seq DESC LIMIT 1").fetchone()
d = json.loads(spec)
h = hashlib.sha256(pathlib.Path("tools/accuracy_registry.py").read_bytes()).hexdigest()
print("seq", seq, "pin", d["graderSha256"][:16], "file", h[:16],
      "MATCH", d["graderSha256"] == h)
PY

# 2. Only then re-grade
ops/accuracy-registry.sh
```

Expected: a new `grading-protocol` record above seq 28 pinning `bcb72256…` at commit
`328d8c4`, `MATCH True`, and the README live-accuracy block replacing GRADING REFUSED
with graded rows — whose directional verdicts are expected to read **FAILED**.

**Do not** hand-edit `graderSha256`. **Do not** widen `MIN_INDEPENDENT_N`,
`MIN_DISTINCT_DAYS`, `MIN_DISTINCT_BLOCKS` or `MAX_ALPHA` to make anything publish.
**Do not** reprint the 2026-08-03 23:06 snapshot as live. If the registrar does not
re-pin, diagnose the registrar — do not shortcut it.

## 12. Separate blocker found (not P1)

`daemon/internal/riskgate/riskgate.go` does not compile in the working tree
(`undefined: req`, `undefined: edge`; uncommitted, +152/−12, with +107 lines of new
tests). Excluded from commit `328d8c4` deliberately.

This blocks any redeploy and is directly relevant to **P4** — the package that does not
build is the risk-sizing package, the same one `ALPHA_WORKFLOW.md` marks "Kelly
sizing — Exists". It should be finished or reverted before P4 begins.
