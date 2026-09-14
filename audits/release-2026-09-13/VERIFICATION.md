# VERIFICATION — every check run, with its result

Candidate: **`de8742b4798f0b9cb7254fdbc6f0a3723859e005`** on `public-launch`.
Web `BUILD_ID` **`AG83roTnCLnAWt5dtSGj0`**. Interpreter `.venv/Scripts/python.exe`
(**Python 3.12.10** — the prescribed one; it launches fine here, contrary to the
baseline audit's sandbox limitation). Go toolchain with `CGO_ENABLED=1` and gcc
present, so the race detector is available.

URL inspected in a real browser: **`http://127.0.0.1:8323`** (the "SignalDeck
Web" instance). `http://127.0.0.1:3000` was checked with the asset tooling but
not browsed.

All checks below were run against the post-repair candidate. No pre-fix result
is reused anywhere.

---

## Required test suites

| Check | Command | Result | Notes |
| --- | --- | --- | --- |
| Go build | `go build ./...` | **PASS** exit 0 | |
| Go vet | `go vet ./internal/api/ ./cmd/collapsecheck/` | **PASS** | |
| Go full suite | `go test ./... -count=1` | **PASS** exit 0 | 0 failures |
| **Go race** | `go test -race -timeout 45m ./internal/api/ ./internal/pipeline/ ./internal/store/` | **PASS**, **0 data races** | api 766 s · pipeline 709 s · store 974 s |
| Go race, rest | `go test -race ./...` | 125 pkgs ok, 0 data races | see caveat below |
| Python suite | `pytest tools/ -q` | **PASS — 451 passed, 0 failed**, 24 subtests, 32 warnings, 153 s | |
| Web typecheck | `npx tsc --noEmit` | **PASS** exit 0 | run after every web edit |
| Web lint | `npx eslint src --max-warnings=0` | **PASS** exit 0 | |
| Web production build | `npm run build` | **PASS** exit 0 | 60 routes generated |
| docs-gate `strict` | `docs_gate.py check` | **exit 1 — 1 violation, CORRECT** | grader REFUSED; strict must refuse to publish numbers |
| docs-gate `release` | `docs_gate.py check --contract release` | **exit 0** | prints `evidence state = REFUSED (measured; graded_at 2026-09-13T14:42:10)` |
| Audit register | `pytest tools/test_audit_register.py` | **PASS** 20 passed | |
| publication_gate selfcheck | `publication_gate.py --selfcheck` | **PASS** — prints `PUBGATE SELFCHECK OK` | |

### Race-detector caveat, recorded because it will be misread

`go test -race ./...` with **default settings FAILS**, and it is not a
correctness problem. `internal/api`, `internal/pipeline` and `internal/store`
each exceed the default **10-minute per-package timeout** under `-race` — they
need 709–974 s. The run reports `FAIL ... 600.xxx s` with **zero** `DATA RACE`
lines. Re-run with `-timeout 45m` and all three pass.

Anyone running the documented command will see three FAILs and conclude there
are races. There are none.

---

## New tests added this pass

| Suite | Assertions | Result |
| --- | --- | --- |
| `ops/test-web-assets-check.ps1` | 4 | **PASS** — isolated build, one chunk deleted |
| `tools/test_publication_gates.py` | 16 | **PASS** — drives the real `ops/grade.sh` in a sandbox |
| `tools/test_docs_gate.py::ReleaseContractTest` | 9 | **PASS** |
| `ops/test-public-profile.sh` | 4 | **PASS** — both profiles, built bundle |
| `daemon/.../collapsegate_test.go` (new case) | 1 | **PASS** |
| `daemon/.../tenantboundary_test.go` | 4 | **PASS** |
| `tools/test_deployment_drift_wiring.py` (1 new) | 1 | **PASS** |

### Mutation checks — each new control was proven to FAIL before the fix

| Fix | Mutation applied | Observed |
| --- | --- | --- |
| F03 handler | restore `err == nil && collapsed` | `/api/accuracy` → **HTTP 200, status OK, `live_acc: 0.51`** published on an ungated window. Test fails. |
| F03 container | restore `log "collapse gate undetermined -- publishing"` | **3 of 16** publication-gate tests fail; log shows `collapse gate undetermined -- publishing` then `grade OK` |
| F01/F02 | delete one CSS chunk from an isolated build | `/login` still **200**; asset check **exit 1** naming the chunk |

A negative control that only asserted failure would also pass against a server
that was simply down, so each asserts the conjunction (the old check still
passes **and** the new one catches it).

---

## Live reproduction evidence

| Measurement | Value | When |
| --- | --- | --- |
| `:3000` assets unserved | **4 of 14**, all present on disk | 21:28 |
| `:8323` assets | 14 of 14 | 21:28 |
| `logs/web-guard.log` | `3000 ok` every 5 min, 21:00→21:25, throughout the outage | from its own log |
| After a real rebuild | **both** instances 200 on `/`, HTTP **500** on 2 assets | 23:05 |
| After guard restart | both **27/27** assets (14 chunk + 13 font) | 23:07 |
| `/api/accuracy` | 503 REFUSED, 18 collapsed of 76 day-horizons | verbatim match to baseline |
| `/api/ledger/verify` | intact, incremental, 501,511 entries, 23 anchors, **0 failing** | grown from 501,271 |
| Anteriority | proven through **#500,887**; **624 entries** beyond it | rendered on `/proof` |
| vol-forecast h=1 | n=3133, 5/60 days, 306 ungradable, INSUFFICIENT | verbatim |
| vol-forecast h=5 | n=300, 1/60 days, 297 ungradable, INSUFFICIENT | verbatim |
| daemon ↔ HEAD diff | `5483350..334152d` = **7 web files, zero daemon/ops/tools** | provenance, not staleness |

---

## Browser inspection — the exact candidate

Real browser, `BUILD_ID AG83roTnCLnAWt5dtSGj0`:

- `/` desktop — styled, refusal banner visible and legible, corrected hero copy
- `/` mobile 375×812 — no horizontal overflow, readable, hierarchy intact
- `/proof` — ledger provenance table renders all five rows; registration section
  renders its honest unavailable state (see limitation below)
- `/volatility` — INSUFFICIENT at both horizons, **no figures shown**
- `/accuracy` — no figures while refused; historical record explains the
  withheld document rather than printing a repo path

---

## NOT RUN / NOT VERIFIED — stated, not implied

| Item | Why |
| --- | --- |
| **Playwright e2e** | The suite drives the **live daemon on :8322 and its real database** (`playwright.config.ts` says so; `smoke.spec.ts` registers and logs in through the proxy). The instruction forbids running the mutation-capable suite against real paper books, and the isolated seeded fixture it asks for was not built. **Another session ran it at 21:15: 39 passed / 4 failed / 2 skipped**, and committed `cf321a23` fixing both failure classes as GATE defects, not product defects. That is its result, not mine. |
| **Docker image build** | docker CLI installed, daemon **not running** on this host. `ARG`→`ENV` was never exercised end to end. The bundle-level behaviour it feeds **is** verified (`ops/test-public-profile.sh`). |
| **Container parity / deployment drift (F06)** | Not attempted. |
| **Ledger write crash-safety (F08)** | Not attempted. |
| **Restore / backup rehearsal** | Not run. |
| **Clean-clone rehearsal** | Not run. |
| **Load / latency measurement** | Not run. No latency claim is made anywhere in this pass. |
| **Dependency/security scan** | Not run. |
| **`/api/prereg` anonymous, live** | The daemon change is **source-only**. The running daemon is revision `5483350`; `/api/prereg` still 401s here until a deploy. Asserted at unit level by `TestProofReceiptsArePublicButNarrowly`. |
| **Keyboard-only traversal, contrast measurement, zoom** | Not measured. Mobile layout was inspected visually only. |
| **Two red scheduled tasks** | `Check-Grader-Health` and `Check-Task-Health` both `LastTaskResult 1`. Not diagnosed. |
| **Per-worker degradation cause** | 3 workers degraded; cause not determined per worker. |

---

## Pitfalls hit during this pass, recorded so they are not re-learned

1. **Pipe-masking exit codes.** `docs_gate.py check | tail` reported `EXIT=0`
   while the gate exited 1. Every exit code in this document was captured
   without a pipe.
2. **`Start-Process -ArgumentList` splits on spaces.** This repo lives under
   `Desktop\claude code`, so an unquoted path reached node as
   `C:\Users\Nicholas_N\Desktop\claude` → `MODULE_NOT_FOUND`.
3. **Heredoc backslash collapse.** A doubled backslash arrives as one; patch
   scripts were written to files instead.
4. **A UTC timestamp read as local** briefly looked like a concurrent session
   writing `ops/data-integrity.json`. It was not.
