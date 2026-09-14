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

## F06 — P2 — container applies one gate fewer than the dev box · CONFIRMED → OPEN

`ops/grade.sh`'s own header documents this: `deployment_drift.py` shells out to
git and there is no checkout in the image, so a stale binary the dev-box
publish refuses on is still graded in the container.

**Not fixed tonight.** The designed remedy (a verified build manifest binding
component revisions and image digest, verified on the build host where git
exists and re-checked inside the container against a trust boundary
independent of a caller-supplied label) is a larger piece than the night had
room for after F01–F05. Recorded as OPEN rather than implied. The divergence
is at least documented in the file itself, and the OCI
`org.opencontainers.image.revision` label + `ops/oracle-verify.sh` already
provide part of the binding.

---

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

## F08 — P2 — best-effort ledger write on the prediction path · CONFIRMED → OPEN

The prediction runner persists prediction, benchmark/features and ledger
through separate writes and ledger failure is best-effort. The crash-safe
redesign (atomic transaction or transactional outbox with eligibility gated
until attested) is genuinely invasive — it touches the single-writer
architecture — and was not attempted overnight. Recorded OPEN. **No backdating
or retrofitting was done**, per the standing rule.

---

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
| `/api/health` degraded, 3 workers degraded | CONFIRMED. **Cause not yet determined per worker — OPEN.** |

## Opened during the pass, not yet closed

| ID | Detail |
| --- | --- |
| F-NEW-02 | `SignalDeck Check-Grader-Health` and `SignalDeck Check-Task-Health` scheduled tasks both report `LastTaskResult 1`. Not diagnosed. |
| F-NEW-03 | Per-worker cause of the three degraded workers not determined. |
| F-NEW-04 | `/api/vol-forecast/record?horizon=5` returns the identical payload as `?horizon=1` — the parameter appears to be ignored (both horizons are returned in a `horizons` array). Contract question, not a data defect. |
| F-NEW-05 | Race detector: 0 data races, but `internal/api`, `internal/pipeline` and `internal/store` exceed the default 10-minute per-package timeout under `-race`. Re-run with 45 min in progress at time of writing. |
