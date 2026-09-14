# FINDINGS — overnight release-candidate pass, 2026-09-13/14

Baseline: read-only Codex audit, 2026-09-13 ~21:00 PT, branch `public-launch`
@ `334152d4`. Every item below was **reproduced before being touched**. Status
is one of CONFIRMED→FIXED, CONFIRMED→OPEN, NOT REPRODUCED, EXPECTED LIMITATION,
BLOCKED.

Priority: P1 blocks a demo or publishes something untrue. P2 is a real defect
with a workaround. P3 is accuracy-of-description.

---

## F01 — P1 — frontend served a 200 with no stylesheet · CONFIRMED → FIXED

**Reproduced** 21:28 PT. `:3000` returned HTTP 200 on `/` (37,131 bytes) and on
`/login`, and 404'd 4 of the 14 build assets the page referenced. All four were
**present on disk** in `web/.next/static`. `:8323` served all 14.

**Root cause — measured, not assumed.** `next start` indexes `.next/static`
once, at boot. Both instances (`ops/start-local-workspace.ps1`, one per port)
serve from the *same mutable* `web/.next`, and `next build` replaces it in
place. Turbopack names chunks by content hash, so a rebuild keeps the filename
of every chunk whose content did not change and mints a new name for each one
that did. An instance that predates the build answers for the former and fails
the latter — which is why the breakage is PARTIAL and invisible to a
single-route status probe. The four failures were not older or newer on disk
than the ten that passed; they were the four whose *names* were new.

The `:3000` instance was pid 10856, started 2026-09-12 21:19 and still running
24 h later: its task is `MultipleInstances=IgnoreNew`, so every later start was
refused `0x800710E0` ("the operator or administrator has refused the request")
and nothing ever replaced it.

**Fix** `532e6a2`. `ops/web-assets-check.ps1` drives off the page's own markup —
fetch the page, fetch every chunk it names, follow each stylesheet to its
`url(../media/*.woff2)` faces, require a sane Content-Type. Exit 1 = up but
incomplete; exit 2 = could not run (kept apart: the caller restarts on one and
pages a human on the other).

**Regression** `ops/test-web-assets-check.ps1`, 4 assertions, isolated build in
`web/.next-test` via the new `SIGNALDECK_DIST_DIR`. It asserts the CONJUNCTION —
`/login` still 200 **and** the check fails — because a test requiring only a
failure would also pass against a server that was simply down.

**Re-verified end to end** after a real build at 23:05: the build broke BOTH
instances (200 on `/`, HTTP 500 on 2 assets), the guard detected and restarted
both, and both returned 27/27 assets. That is the build/restart rehearsal.

---

## F02 — P1 — the web guard could not see F01 · CONFIRMED → FIXED

**Reproduced from its own log.** `logs/web-guard.log` carried `3000 ok` on
every five-minute cycle from 21:00 to 21:25 while that instance served an
unstyled page. `ops/web-guard.ps1` probed only `GET /login` for a 2xx/3xx.

**Fix** `532e6a2`. Liveness (cheap, unchanged) and completeness (the shared
check above) are now separate questions. An INCOMPLETE instance is restarted —
a restart re-indexes `.next`, which is the remedy. Still incomplete 45 s after
a full restart means the build on disk is bad and another restart cannot fix
it, so it is reported and left alone rather than flapped every five minutes.
Browser-level checks deliberately stay out of a job that runs every 5 minutes.

**Self-inflicted defect found and fixed in the same commit.** My first version
polled `/login` then asset-checked ONCE, the instant anything answered. The
21:28:08 restart succeeded, `/login` answered 3 s later from the OLD process
still shutting down, the single check ran against it, and the guard filed "back
but STILL INCOMPLETE" for a server that measured healthy 9 s later. It now
polls the condition it actually cares about.

---

## F03 — P1 — the collapse gate published when it could not run · CONFIRMED → FIXED

**Reproduced by mutation.** `daemon/internal/api/accuracy.go` tested
`err == nil && collapsed`. With that line restored, `GET /api/accuracy` on a
window whose gate cannot be evaluated returns **HTTP 200, status OK, carrying
`live_acc: 0.51`** — publishing figures the gate exists to withhold.

`cmd/collapsecheck` documented exit 2 as "the caller must FAIL OPEN and
publish", and both shell wrappers obeyed. The stated reason was transient DB
errors — but a missing binary, a renamed flag and a bad `--db` path all exit 2,
and each un-wires the gate permanently and silently. The precedent was already
in the same file: `deployment_drift.py` refuses on ANY non-zero exit, citing
`ops/research-liveness.sh` passing a flag that never existed, argparse exiting
2, and the check having "never produced a verdict".

**Fix** `67d69e2`. All three withhold, and keep the two outcomes apart:
`REFUSED` = measured and found collapsed; **`REFUSED_UNAVAILABLE`** (new) =
nobody measured. Fusing them would fabricate a scientific verdict every time a
read timed out — the same dishonesty as publishing, pointed the other way.

---

## F04 — P1 — a crash and a row refusal were the same byte · CONFIRMED → FIXED

`tools/selection_honesty.py` ends `return 1 if refused else 0`; an unhandled
exception also exits 1. Both wrappers discarded it (`|| true`; a log line
reading "row-level refusals are expected"). A traceback halfway through the
merge published a half-merged registry exactly like a clean run.

**Fix** `67d69e2`. The exit code is still discarded — a refused row is a
disclosure, not an outage, and the suite asserts a row refusal still PUBLISHES.
The ARTIFACT is now checked instead: `tools/publication_gate.py` requires every
directional row carrying breadth tallies to have an honesty block, from the
right tool, with the right keys. A crash cannot answer that yes.

---

## F05 — P1 — "grade OK" was printed whether or not the heartbeat was written · CONFIRMED → FIXED

`ops/grade.sh` called `grader_heartbeat.py --success`, ignored the result, and
printed `grade OK`. `/api/accuracy` answers `REFUSED_STALE` when that heartbeat
ages past `GraderMaxAge`, so a silently failed write means the registry carries
today's verdicts while the API refuses them.

**Fix** `67d69e2`. Both wrappers check it. The dev-box script stops BEFORE
regenerating README and the eight INCLUDES documents, because publishing a
verdict table the API refuses is the two-surfaces divergence the collapse gate
exists to prevent, arriving by another door.

---

## F-NEW-01 — P1 — publication was never atomic · CONFIRMED → FIXED

Not in the baseline; found while reading the chain. Every gate ran against
`$OUT` *itself*, and `$OUT` is the file the daemon serves —
`loadRegistry()` does an `os.ReadFile` on **every request**. The pinned
grader's raw output became the public registry the instant it was written and
stayed public for as long as the merge, the freshness assertion and the
collapse gate took to run. `selection_honesty --merge` reopens the same path
`"w"`, so a reader could also catch it truncated.

**Fix** `67d69e2`. Built in a staging file, all gates run against the staging
file, `$OUT` replaced by one atomic rename. No historical verdict changed.

---

## F06 — P2 — container applies one gate fewer than the dev box · CONFIRMED → FIXED

`ops/grade.sh`'s own header documents it: `tools/deployment_drift.py` shells out
to git and `.dockerignore` excludes `.git`, so "a stale binary the dev-box
publish refuses on is still graded here."

**Why the obvious patch is worthless.** `ops/docker-build.sh` passes
`--build-arg GIT_REV="$rev"` and says in as many words that the path is
"TRUSTED, not checked". An image that records a caller-supplied label and reads
it back proves only that someone typed it.

**Fix** `0000059`. What crosses the boundary is *content*. The files that decide
a verdict are copied into the image byte-for-byte, so `tools/build_manifest.py`
hashes them on the **host** (where git exists), a Dockerfile `RUN` **seals** the
binaries compiled during the build, and `ops/grade.sh` **re-hashes and compares**
before grading. Forge `GIT_REV`, ship different `tools/`, and the hashes
disagree.

**What it refuses to claim:** that the revision was *verified*. Nothing in the
image can resolve a commit, so every run prints
`revision <sha> RECORDED (not verifiable here: no git in this image)`.
Inventing resolvability would be the same dishonesty as the fail-open gates,
wearing a provenance costume.

Exit codes keep the findings apart: **1** = the check ran and the bytes disagree
(an accusation naming a file); **2** = the check could not run (an outage).

**Regression** 6 new controls in `tools/test_publication_gates.py` (22 total):
swapped grader, swapped protocol document, missing manifest reported as an
outage *not* an accusation, a manifest pinning nothing, a forged revision that
neither rescues tampering nor breaks an honest image, and verify never claiming
the revision was checked. **Mutation check:** forcing `manifest_status=0` fails
3 of 22.

**NOT VERIFIED:** no image was built — docker CLI present, daemon not running —
so `seal` has never executed inside a real build. Emit was exercised end to end
against this repository (6 pinned, 0 missing, revision resolvable, clean tree).

## F07 — P1 — the public-mode flag was documented, read twice, and unreachable · CONFIRMED → FIXED

`NEXT_PUBLIC_SIGNALDECK_PUBLIC` is in `.env.example`, DEPLOY.md tells the
operator to set it, and it is read by `AuthGate.tsx` (where a 401 sends an
anonymous visitor) and `proof/page.tsx` (operator-only copy). **The Dockerfile
had no ARG for it** — and `NEXT_PUBLIC_*` is inlined by the compiler, which
that same file says four lines above about `NEXT_PUBLIC_SITE_URL`.

**Measured in the bundle:**

```
unset:  401===a.status?window.location.replace("1"===t.default.env.NEXT_PUBLIC_SIGNALDECK_PU...
=1:     401===t.status?window.location.replace("/")
```

Unset, the compiler leaves a RUNTIME lookup. `process.env` is empty in a
browser, so that comparison is against `undefined`, false forever, and every
anonymous visitor is bounced to `/login`. The source looks conditional; the
artifact is not.

**Fix** `2901422`. ARG added, defaulting to private (safe direction).
`ops/docker-build.sh` refuses to build an image carrying a real hostname
without an explicit audience, and prints the chosen audience on every build.
`ops/test-public-profile.sh` asserts the behaviour in the built bundle, not the
Dockerfile's text — 4 assertions, both profiles, isolated `distDir`.

**NOT VERIFIED:** the Docker leg itself. The docker CLI is installed but the
daemon is not running on this host, so ARG→ENV was never exercised end to end.

---

## F08 — P1 — a forecast could be graded as precommitted with no attestation · CONFIRMED → FIXED

Raised from P2 once measured. The prediction runner wrote the prediction and its
chain entry separately, with the ledger half explicitly best-effort. But
`UpsertPrediction` *also* seeds `prediction_outcomes` — which `predict.go`
itself calls "the population every grader reads" — so a forecast whose
attestation failed was still graded later as though it had been committed to the
chain before its outcome existed. **Precommitment is the claim the whole project
rests on.**

**Measured 2026-09-14T06:03Z** (full cohort discussion in `LEDGER_COVERAGE.md`):

| | |
| --- | --- |
| cohort | model horizons only (1d/1w), `n_used > 0`, `ts >= 2026-07-04` |
| denominator | 501,495 eligible rows |
| **unattested** | **31** (0.0062%) |
| **of those, already resolved** | **29** — gradable as precommitted, nothing on the chain behind them |
| span | 2026-07-06 → 2026-09-11 |

`ops/check-grader-health.ps1` was reporting this as a bounded alarm (max 50).
**An alarm is not a gate.**

**Fix** `03d3b4a`. `store.UpsertPredictionAttested` writes prediction, chain
entry and eligibility in **one transaction** on the single writer — a failed
append rolls back all three. That is the same argument `UpsertPrediction`
already makes about `n_used > 0`: enforce it *here* and no code path can produce
a gradable forecast that was never attested.

**One `BeginTx`, deliberately.** The writer pool is `MaxOpenConns=1`, so calling
`AppendLedger` from inside an open transaction would wait forever on the
connection it already holds — the nested-transaction deadlock the instruction
names. The chain link is *shared* via `appendLedgerTx`, not reimplemented,
because two copies of the hashing rule drift.

**Regression** 5 tests in `daemon/internal/store/attestation_test.go`. Mutation
check: restoring best-effort fails `TestAttested_LedgerFailureLeavesNothingGradable`
with "the append failed but the write reported success".

**History untouched.** The 31 stay exactly as they are — backdating them would
fabricate evidence in the one place the project claims cannot be fabricated;
deleting them would break the chain linkage; re-grading to exclude them would
change published verdicts. Classified, not repaired. No verdict was recomputed.

## F09 — P1 — /proof claimed a walk it never made · CONFIRMED → FIXED

The page said: *"recomputed just now, top to bottom, with no break. A
prediction can't be edited or back-dated after the fact."* The response that
produced that sentence says `incremental: true` and, verbatim, that intact
means *"the stored rows are internally consistent — NOT that they were written
when they claim"*. Two sentences, one page, opposite claims.

**Root cause at the client boundary.** `LedgerVerifyResponse` declared four
fields and stopped, so `incremental`, `intactMeans` and the whole
`tamperEvidence` block were discarded on arrival. The page had `intact: true`
and nothing else and filled the gap with confidence. The daemon had been
scrupulous the entire time and nothing could see it.

**Fix** `e9c0900`. Type carries them; a new `LedgerProvenance` section renders
what was actually checked. Live, in a browser:

| | |
| --- | --- |
| Verification mode | incremental — only rows after the last checkpoint were re-hashed |
| Anchor check | 23 anchor(s), against STORED head hashes |
| Anteriority proven through | entry #500,887, signed 2026-09-14 02:47Z |
| **Carries no anteriority proof** | **624 entries** appended after that anchor |
| Anchors that stopped reproducing | none |

624 entries with no anteriority evidence is a fact the old page actively
concealed while claiming the opposite.

---

## F10 / F12 / F13 — P1/P2 — product honesty and evidence navigation · CONFIRMED → FIXED

`e9c0900`, plus nav/eyebrow labels in a follow-up. Five claims corrected:

1. *"There is no order path, no portfolio, no execution and no position sizing
   anywhere on this site."* **FALSE** — `/lab/paper` posts to
   `/api/paper/order`, `/lab/portfolio` is a route. A false modesty claim on
   the landing page of an honesty product is worse than what it denied.
2. *"Volatility and loss estimates … how much you could lose on a bad day."*
   The word "loss" on that page means **QLIKE**, the statistical loss function
   the forecast is scored under. `/api/vol-forecast/record` carries
   `meanQlike`, `vsEwma`, `vsRandomWalk` and a verdict, and has **no quantile,
   VaR or drawdown field anywhere**. On a finance site that title reads as
   money. Retitled across metadata, hero, cards, nav label and page eyebrow.
3. *"Two new estimates are being pre-registered."* They are registered and
   accruing; what they lack is independent trading days.
4. *"Every predictor … has FAILED against its own null."* Collapsed retirement
   on measured evidence, a result indistinguishable from chance, and a figure
   WITHHELD into one word — and the repo's own record says the directional book
   is indistinguishable from chance rather than anti-predictive, so "failed"
   overstated even the measured part.
5. *"it cannot quietly replace the original"* — stronger than the chain proves
   and stronger than `/api/ledger/verify` says about itself. Scoped to
   tamper-EVIDENCE plus externally published anchors.

**Evidence navigation.** `/volatility` said "you can read that registration on
the receipts page", `/accuracy` printed a bare `proofs/*.md` path in a `<code>`
tag, and `/proof` rendered no registration at all. `GET /api/prereg` has
carried all 116 records the whole time and **no page had ever called it**.
`/proof` now renders it. One daemon change was needed: `/api/prereg` was in
`publicRoutes` but missing from the narrow receipts exemption that keeps
`/proof` working when `PublicReads` is closed.

The accuracy page's document is **deliberately still not served** — it holds the
dated pre-epoch grade, and publishing it would reprint the exact figures the
current window is refusing. The page now says so and links to the registered
rule instead.

---

## F11 — P1 — one gate answering two different questions · CONFIRMED → FIXED

`tools/docs_gate.py check` required grader status OK, so the honest REFUSED
this repository has carried since 14:43 blocked the release gate permanently —
pressuring someone to make the refusal disappear rather than ship it.

**Fix** `67d69e2` + `docs/RELEASE_CONTRACTS.md`. Two contracts, one gate.
`strict` is unchanged in rigor and is what every historical report meant.
`release` accepts only a **current, measured, attributed** refusal and rejects
three impostors by name: `REFUSED_STALE`, a `CHECK UNAVAILABLE` outage, and a
refusal with no reason. Neither contract relaxes the figure-leak checks, and
the scientific state is printed separately on **every** run including a clean
one, so a green release build cannot read as "the models work".

Measured on the live repo: `strict` exit 1, `release` exit 0, both printing
`scientific evidence state = REFUSED (measured; graded_at 2026-09-13T14:42:10)`.

---

## Baseline items NOT reproduced

| Item | Result |
| --- | --- |
| `.venv/Scripts/python.exe` could not launch | **NOT REPRODUCED.** Runs fine here: Python 3.12.10. The audit's sandbox limitation is not a property of this machine. |
| Daemon revision `5483350` ≠ HEAD implies stale daemon code | **EXPECTED LIMITATION, confirmed.** `git diff 5483350..334152d` touches **7 web files only**, zero `daemon/`, `ops/`, `tools/` or Dockerfile. A release-provenance question, exactly as the audit said — not stale daemon code. |
| Ledger entry count 501,271 | Grown to 501,511 during the pass. Live system; expected. |
| 23 anchors, 0 failing | CONFIRMED, unchanged. |
| `/api/accuracy` 503 REFUSED, 18 collapsed of 76 | CONFIRMED verbatim. |
| vol-forecast h=1 n=3133/5 days/306 ungradable; h=5 n=300/1 day/297 | CONFIRMED verbatim, both INSUFFICIENT. |
| `/api/health` degraded, 3 workers degraded | CONFIRMED, and **diagnosed per worker — see F-NEW-03. Not a defect:** all three ran and deliberately declined to deliver. |

## Opened during the pass, not yet closed

| ID | Detail |
| --- | --- |
| F-NEW-02 | **CLOSED.** Both health tasks were red. **Root cause was not a code defect:** the System log shows 6006 at 2026-09-11 00:21 and 6005 at 2026-09-12 21:18 — the machine was **off for ~45 h**. `SignalDeck Accuracy` fires daily at 14:05 with `WakeToRun=False`, so it could not run on either day; `grader_heartbeats` confirms rows on 09-10 and 09-13 with nothing between. The heartbeat reached 67 h against a 26 h ceiling, `Check-Grader-Health` went red at 09:20 on 09-13, and `Check-Task-Health` went red behind it as a pure cascade (it reads the other task's stored `LastTaskResult`). The grader itself ran normally at 14:05 that day. **Both checks were correct; what they could not do was say which cause applied.** Fixed in `cff7012`: a stale heartbeat now consults uptime, forgiven only while uptime is under one window and never past a 3× ceiling, with an unreadable uptime never an excuse — 10 assertions in `ops/test-check-grader-health.ps1`. Both tasks now record `0x00000000`, verified by running them. |
| F-NEW-03 | **CLOSED — not a defect.** The baseline asked to "determine actual cause per worker"; all three file `workers.ErrDegraded`, which `daemon/internal/api/api.go` documents as "the status a worker files when it RAN and chose not to deliver". Read from `worker_runs` rather than inferred: **gbm-trainer** — "no model leg cleared its OOS edge bar"; **expectancy-trainer** — "anti-predictive and benched fleet-wide (1d AUC 0.4581, 1w AUC 0.4548)"; **forecast-monitor** — coverage starvation of the flagship model **retired 2026-07-24**, which `forecastmon.go` itself calls out: "a RETIRED model that declines the cross-section is doing what retirement means; erroring on it every run holds the daemon red indefinitely on a condition that is not a fault". `/api/ready` correctly stays `ready: true` while `/api/health` correctly reports `degraded: true`. This is the honesty machinery working, not three broken workers. The set also moves on its own — expectancy-trainer was `running` again by 06:10Z. |
| F-NEW-04 | `/api/vol-forecast/record?horizon=5` returns the identical payload as `?horizon=1` — the parameter appears to be ignored (both horizons are returned in a `horizons` array). Contract question, not a data defect. |
| F-NEW-05 | **RESOLVED, not a defect.** Race detector finds **0 data races**. `internal/api`, `internal/pipeline` and `internal/store` exceed the default 10-minute per-package timeout under `-race` (they need 709–974 s). With `-timeout 45m` all three pass. Anyone running the documented command sees three FAILs and concludes there are races; there are none. Grep for `DATA RACE` before believing a race failure. |

---

## Closing state of this pass

**No finding is left in CONFIRMED → OPEN.** Of the sixteen items tracked:

- **13 CONFIRMED → FIXED**, each with a regression test, and the six that
  guard an evidence boundary each additionally mutation-checked (the control
  was shown to fail before the fix, for the right reason).
- **2 NOT REPRODUCED / EXPECTED LIMITATION** — the prescribed venv runs fine
  here, and the daemon/HEAD revision difference is release provenance rather
  than stale daemon code (7 web files, zero daemon/ops/tools).
- **1 CLOSED as not-a-defect** — the degraded workers above.

What remains is **not defects but unexercised verification**, listed in
`VERIFICATION.md`: no Playwright run of my own, no Docker image, no restore or
clean-clone rehearsal, no load measurement, no dependency scan. And the daemon
changes in this candidate are source-only until someone deploys them.
