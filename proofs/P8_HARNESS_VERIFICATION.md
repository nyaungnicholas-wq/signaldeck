# P8 — Harness verification: every gate run, and two defects it exposed

**Filed:** 2026-08-04T18:0x-07:00
**Phase:** P8 (CI / automated proof harness)
**Relationship to `P8_DOCS_GATE.md` / `P8_TEST_OUTPUT.txt`:** those record the docs
gate. This records an end-to-end **execution** of the whole harness by a second agent,
and the two defects that execution exposed.

The premise: a declared gate and a passing gate are different things, and this
repository's own history is a list of gates that existed and never ran — the dead A4
gate floor, the unwired A13 scan, `TradableAt` returning empty for its entire life,
`MarkDelisted` with no callers.

---

## 1. Every gate, run locally

| Gate | Command | Result |
|---|---|---|
| Go build | `go build ./...` | **exit 0** |
| Go vet | `go vet ./...` | **exit 0** |
| Go tests | `go test ./...` | **exit 0**, full daemon suite |
| Python tools | `python -m unittest discover -s tools` | **272 tests, OK** (4 skipped) |
| Docs gate | `python tools/docs_gate.py check` | **exit 0** — `docs-gate: clean` |
| Live-record drift | `live_accuracy.py --check --inject $(cat INCLUDES.txt)` | **exit 0** |
| Live-record scan | `live_accuracy.py --scan $(git ls-files '*.md')` | **exit 0** |
| SPA ledger | `python tools/check_spa_ledger.py` | **exit 0** — 48 rules, StepM survivors 2, Bonferroni 0 |
| Ledger provenance self-test | `ops/test-ledger-provenance.sh` | **exit 0** — 5/5 |
| Manifest | `ops/manifest-check.sh` | **exit 0** — *after §2* |
| Secret-scanner self-test | `ops/pre-publish-scan-test.sh` | **exit 0** — 8/8, *after §3* |
| Pre-publish scan | `ops/pre-publish-scan.sh` | **exit 0** — SAFE TO PUBLISH |
| Web lint | `npm run lint` | **exit 0** |
| Web build | `npm run build` | **exit 0** — full production build |

Two of these did not pass when first run. Both are below.

---

## 2. Defect 1 — the entire remediation was untracked

`ops/manifest-check.sh` failed with ~17 offending paths. The cause was not a stray
file: **P0–P7's deliverables existed only in a working tree.** A clone of HEAD
contained none of it.

Among the untracked paths:

| Path | What it is |
|---|---|
| `daemon/internal/killswitch/` | **P4C's kill switch** — code + tests, passing |
| `RISK_POLICY.md` | **P4A** — the policy whose absence scored 0/25 |
| `EXECUTION_SPEC.md` | **P4B/P5** — risk-before-admission, EV gate, exits |
| `STRATEGY_DECK.md` | **P7** — the corrected deck |
| `daemon/internal/store/pituniverse.go` | **P3B** — the PIT universe rebuild |
| `tools/live_accuracy.py`, `partials/` | **P2** — the single source of truth |
| `proofs/` | every proof artifact for every phase |

The kill switch is the sharpest case. `INSTITUTIONAL_GAP.md` was faulted in the review
that opened this remediation for claiming a kill switch by citing
`stock-trader/trader/risk_gate.py` — a control in a **different repository** that cannot
halt this daemon. P4C corrected that by building a real one. Untracked, it reproduced
the identical defect one level up: a control asserted in documentation that a clone does
not contain. **Rule 1 is explicit — do not claim an execution/risk control exists unless
it is in this repo and tested.** It was tested. It was not in the repo.

Rule 4 says the same thing generally: a fix with no artifact in the repository is still
broken.

**Resolved across five commits**, each verified before staging, never swept:

| Commit | Contents |
|---|---|
| `b563580` | kill switch, live-record generator + tests, partials, proofs |
| `639b016` | P0–P7 deliverables, specs, PIT universe, docs gate, git hooks (86 files) |
| *(spa)* | `tools/spa_ledger_result.json` — cited by P3A, consumed by `check_spa_ledger.py` |
| `e513ac3` | the web surface, after its own lint/build gates were run (41 files) |
| *(proofs)* | P8/P9 artifacts |

`tools/docs_gate.py` was deliberately **excluded** from the first of these: at that
moment it did not parse (`SyntaxError: '{' was never closed`) and was failing 43 of its
own tests while being actively written. A publication gate that cannot pass its own
suite must not enter history, and repairing another session's in-flight file would have
corrupted it. It was included in `639b016` only once it passed — 49 tests, OK.

**Result:** `MANIFEST OK — a fresh clone contains every load-bearing path.`

---

## 3. Defect 2 — the secret scanner's self-test cried wolf

`ops/pre-publish-scan-test.sh` reported **5 passed / 3 failed**. CI's `pre-publish-scan`
job runs it, so that job was red.

**None of the three was a scanner defect.** The scan gained a manifest section that runs
`"$(dirname "$0")/manifest-check.sh"` and treats a non-zero exit as *"load-bearing paths
are missing from git"*. `init_repo` builds a throwaway fixture containing only
`pre-publish-scan.sh`, so in the fixture that path did not exist:

```
── 5. Manifest ────────
  /bin/bash: ./ops/manifest-check.sh: No such file or directory
  ✗ load-bearing paths are missing from git
```

bash's "No such file or directory" was read as a manifest **failure**, so every case
expecting a clean tree failed for a reason unrelated to secrets:

```
✗ clean tree: expected exit 0 + SAFE TO PUBLISH, got exit=1
✗ fake credential in _test.go: expected exit 0, got exit=1
✗ 6a excluded fixture path: expected exit 0, got exit=1
```

**"The checker is absent" and "the manifest failed" are different facts.** Conflating
them produced a gate that cried wolf — and a harness whose self-test fails for a bogus
reason is one people learn to ignore, which is precisely how unwired-gate defects
survive here.

Fix (commit `7941121`): `init_repo` now provisions a stub `manifest-check.sh` that
exits 0. Section 5 stays structurally intact and is scoped out of a three-file throwaway
repo that could never satisfy this repository's manifest anyway. **The real gate is
untouched** — `ops/pre-publish-scan.sh` still runs the real checker here and in CI.

**After: 8 passed, 0 failed.** Every detection case still passes — uppercase
project-shaped secret in a tracked file, tracked `daemon/.env`, a secret deleted in a
later commit and caught by the history scan, and the path-scoped fixture exclusion with
its neighbour and history controls. The fix removed a false negative in the *test*, not
a true positive in the *scanner*.

---

## 4. What P8 still does not have

- **CI has never actually run any of this.** Every result above is a local run on one
  machine. The `pre-publish-scan` job was red until §3; whether it now passes on a
  hosted runner is unverified, and that is the single largest remaining unknown in P8.
- **The local frozen-evidence capture is deliberately not in git** — 4.7 GB across 12
  files, because it contains a copy of the database. It is now ignored explicitly.

  Worth recording *why* that line exists: naming that directory as a bare path in this
  very document made `ops/manifest-check.sh` demand it, because the manifest reads
  tracked markdown for referenced paths and cannot tell a shippable path from a
  forensic one. The gate's own advice — *"fix by tracking them; never by deleting the
  reference"* — is right for a source path and catastrophic for a multi-gigabyte
  snapshot. Ignoring it is the third option the message does not offer, and the
  proof of a freeze belongs in `proofs/` while the freeze itself stays on the machine
  that took it.

### Closed during this phase — the docs gate is now wired

`tools/docs_gate.py` was passing (`docs-gate: clean`) while **no CI job invoked it** —
the same "exists but never wired" shape this phase exists to prevent. Its job was
authored but sat uncommitted in `.github/workflows/ci.yml`, so a clone received the gate
without the job that runs it.

Landed in commit `10875b2`. The job self-tests before trusting itself
(`python3 tools/test_docs_gate.py`, 49 tests) for the same reason
`pre-publish-scan-test.sh` runs before `pre-publish-scan.sh`, and it treats a missing or
stale `ops/data-integrity.json` as a **violation, never a skip** — the split that lets a
4 GB gitignored database be verified on a runner must not be able to degrade into
silence.

## 5. Status

| P8 done-when | Status |
|---|---|
| Gates exist | **MET** — 14 enumerated in §1 |
| Gates actually run | **MET locally** — all 14 executed, results recorded |
| Gates pass | **MET** — after fixing the two defects in §2 and §3 |
| Recurrence prevented | **MET** — docs gate wired (`10875b2`); manifest, scanner self-test, ledger provenance and docs gate all self-test before trusting themselves |
| Verified on a CI runner | **NOT MET** — every result here is local (§4) |
