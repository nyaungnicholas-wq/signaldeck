# RELEASE MATRIX — five separate verdicts

Candidate **`03d3b4a`** · web `BUILD_ID AG83roTnCLnAWt5dtSGj0` · daemon **running
revision `5483350`** (source-only daemon changes in this candidate are NOT live).

These are deliberately separate. A research demo can be engineering-ready while
its science is insufficient — that is this project's whole posture — but the two
must never be reported as one number.

---

## 1. ENGINEERING READY — **PASS (local candidate only)**

| Evidence | |
| --- | --- |
| Go build, vet, full suite | PASS |
| Go race, 3 heavy packages, 45 min | PASS, **0 data races** |
| Python suite | **457 passed, 0 failed** |
| Web tsc / eslint / production build | PASS |
| Frontend asset integrity, both instances | 27/27 assets each |
| Build→break→guard→repair rehearsal | PASS, measured |
| Tenant isolation (A vs B, logout, anonymous) | PASS, 4 tests |
| Forecast attestation atomicity | PASS, 5 tests, mutation-checked |
| Container build provenance | PASS, 6 tests, mutation-checked |
| Grader-health staleness rule | PASS, 10 assertions |
| New regression tests | 64 assertions across 10 suites |

**Qualifiers.** No Playwright run of my own (reason in VERIFICATION). No clean
clone, no restore rehearsal, no load measurement, no dependency scan. Three
workers report degraded and the per-worker cause is still unknown.

**Closed since the first pass:** both red scheduled tasks (root cause was ~45 h
of machine downtime, not a code defect — `cff7012`), container gate parity
(`0000059`), and the unattested-forecast path (`03d3b4a`).

---

## 2. EVIDENCE PRESENTATION READY — **PASS**

The surfaces now say what the system actually measured.

- `/proof` states verification mode, anchor coverage and that **624 entries
  carry no anteriority proof**, instead of claiming a full walk and
  impossibility of backdating.
- The landing page no longer denies an order path that exists, no longer
  promises "how much you could lose on a bad day" over a QLIKE loss function,
  no longer says claims are "being" pre-registered, and no longer files
  retirement, chance and withheld under one word.
- `/volatility` is named and described as the record it is.
- `/accuracy` explains the withheld document instead of printing a repo path.
- The pre-registration chain is rendered for the first time.
- `docs-gate --contract release` prints the scientific state on **every** run,
  including clean ones, so a green build cannot read as "the models work".

**Qualifier.** On this private deployment the registration section renders its
honest *unavailable* state, because the exemption that fixes it is source-only
until the daemon is deployed.

---

## 3. SCIENTIFIC SKILL ESTABLISHED — **FAIL (and correctly so)**

No skill is established, and nothing in this pass attempted to change that.

| | |
| --- | --- |
| Directional accuracy | **REFUSED** — 18 of 76 graded day-horizons collapsed. Withheld. |
| Flagship directional model | **RETIRED** 2026-07-24 by pre-registered rule; does not lapse |
| Volatility h=1 | **INSUFFICIENT** — 5 of 60 required trading days |
| Volatility h=5 | **INSUFFICIENT** — 1 of 60 |

No sample floor lowered, no settled refuted search re-run, no null changed after
results, no window redefined, no symbol removed, no sealed holdout opened, and
`tools/accuracy_registry.py` was not edited. The repairs made publication
*stricter*, not looser.

This is a legitimate shipping state for a research instrument. It is not a
shipping state for any claim of predictive skill, and no such claim is made.

---

## 4. PUBLIC DEPLOYMENT VERIFIED — **NOT VERIFIED**

Nothing was deployed, and nothing here authorises it.

- No Docker image built — docker daemon not running on this host
- No external anonymous check of any public URL
- No DNS, hosting, or repository-visibility change
- The public-profile fix is verified **at the bundle level**, not through an
  actual image
- **F06 is closed in source** (`0000059`): the image is now bound to its
  reviewed source by content hash, verified before every grade. But `seal` has
  never executed inside a real `docker build`, so the container leg of that fix
  is itself unexercised

No public URL may be called working until someone fetches it anonymously from
outside this machine.

---

## 5. COMPETITION ELIGIBILITY CONFIRMED — **BLOCKED**

Blocked on facts an agent cannot establish, not on engineering.

| Unresolved | |
| --- | --- |
| Which competition | Not named in the instruction; CAC used provisionally |
| **Pre-existing projects** | Could not extract the rule from the official rulebook. SignalDeck predates any submission. **If prior work is excluded, no engineering makes it eligible.** |
| Student status, district, district participation | `[NICHOLAS TO CONFIRM]` |
| Personal contribution | `[NICHOLAS TO CONFIRM]` — an overnight agent pass cannot establish it |

Confirmed from official sources: deadline **2026-10-26**, video **1–3 minutes**
public on YouTube/Vimeo, **AI permitted but must be fully disclosed**, teams up
to 4.

---

## Evidence-integrity changes made after the first pass

Three gates that could not previously fail now can, and each is mutation-checked:

| Gate | Before | After |
| --- | --- | --- |
| Container build provenance | not applied at all (no git in image) | bound by content hash, host-emitted, sealed in-image, verified before each grade |
| Forecast attestation | best-effort; a failed chain append still produced a gradable row | one transaction — a failed append leaves nothing to grade |
| Grader-health staleness | a powered-off machine and a broken grader were indistinguishable | uptime-bounded, ceiling-capped, unreadable uptime is not an excuse |

`LEDGER_COVERAGE.md` records the 31 historical unattested rows with their
cohort, denominator and timestamp. They were **classified, not repaired** — no
backdating, no deletion, no recomputed verdict.

## Overall

Local release candidate: **engineering-ready and honest about its evidence**.
Public deployment: **not verified**. Scientific skill: **not established, and
visibly so**. Competition eligibility: **blocked on owner facts**.
