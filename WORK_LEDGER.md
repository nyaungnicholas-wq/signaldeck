# SignalDeck Work Ledger

Controller-owned. One row per work item. `PASS` requires evidence (command + exit
code). `BLOCKED` requires an external prerequisite and the exact resume action.
No secrets in this file.

Opened: 2026-08-02 17:33 PDT (= 2026-08-02 20:33 America/New_York)
Branch: `audit/2026-07-27` @ `796eaa2`
Repo: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`

## Environment ground truth (verified, not assumed)

| Fact | Value | Evidence |
|---|---|---|
| Go required | `go 1.25.0` | `daemon/go.mod` |
| Go installed | `go1.26.5 windows/amd64` | `go version` — satisfies the directive, no toolchain defect |
| Physical disks | **exactly one**: `KINGSTON OM8TAP42048K1-A00`, SSD, 1907.7 GB, DeviceId 0 | `Get-PhysicalDisk` |
| Partitions | all on DiskNumber 0; `C:` = disk 0 partition 3 | `Get-Partition` |
| Database | `data/signaldeck.db`, 2,957,795,328 bytes, on `C:` | `ls -la data/*.db` |
| Market session at open | Sunday 20:33 ET — **equity market closed** | system clock |

## Status

| # | Item | Status | Evidence |
|---|---|---|---|
| 1 | Offsite backup on separate hardware | **BLOCKED** | Only one physical disk exists (above). Any backup target is necessarily the same physical volume. |
| 2 | Digest helper hashes file contents | **PASS (was already green)** | `cd tools && python -m unittest test_accuracy_registry` → exit 0, `Ran 109 tests ... OK (skipped=4)`. Verified genuinely satisfied, not weakened — see note. |
| 3 | eslint scratch files | **PARTIAL — BLOCKED on concurrency** | Kit.tsx defect fixed and verified. Remaining errors are in another session's live working set. |
| 4 | Pre-publish scan / untracked paths | **NOT STARTED (gated by 3)** | Depends on a stable `web/` tree. |
| 5 | Live equity ticks land in DB | **BLOCKED (time)** | Requires Mon–Fri 09:30–16:00 ET. Next window: Mon 2026-08-03 09:30 ET. |
| 6 | Forced reconnect, no duplicate subs | **BLOCKED (time)** | Same window as Item 5; must run in a live session. |
| 7 | Reverify revision resolvability | **ROOT CAUSE FOUND, not yet fixed** | `daemon/internal/api/version.go:27` |
| 8 | Supervise `tickstreamd` + backups | NOT STARTED | Reboot-survival test needs owner authorization. |
| 4.1–4.5 | Go daemon refactor (Phases 4) | NOT STARTED | Requires baseline benchmark first. |
| 5.A–5.E | Quant/data audit (Phase 5) | NOT STARTED | |
| 6 | Research direction (Phase 6) | NOT STARTED | Treat prior ensemble path as closed. |

---

## Item 1 — BLOCKED: separate physical backup device required

Not a software defect. `Get-PhysicalDisk` returns exactly one device (DeviceId 0,
KINGSTON OM8TAP42048K1-A00, 1907.7 GB SSD). Every partition — including `C:`,
which holds `data/signaldeck.db` — is on DiskNumber 0. There is no second
physical device attached, so no path on this machine can satisfy
`offsiteSameVolume: false`. Running `ops/signaldeck-backup-offline.sh` against
any local folder would produce a same-volume copy and a false green.

**Not faked, not worked around.** Resume when an external drive is attached:

```bash
SIGNALDECK_OFFSITE_DIR=<external-drive-path> bash ops/signaldeck-backup-offline.sh
```

then confirm `/api/quality.ops` reports `offsiteSameVolume: false`.

## Item 2 — PASS, and the guard is intact

The prompt's snapshot said this test fails. It does not; it passes on the
current tree. Confirmed it is genuinely satisfied rather than weakened:

- The test computes `want` with Python `hashlib.sha256(open(target,'rb').read())`,
  sources the real helper block out of `ops/accuracy-registry.sh`, runs
  `sha256_of` against `PREREGISTRATION.md`, and `assertEqual(want, got)`.
  The assertion is present and strict.
- `ops/accuracy-registry.sh:42-45` implements `sha256_of` as shasum → sha256sum →
  a Python `hashlib.sha256(open(...,'rb').read())` fallback. It hashes file bytes.
- It is not one of the 4 skips: `python -m unittest
  test_accuracy_registry.ProtocolDocumentGateTest -v` runs it and reports `OK`.

Nothing to change. No threshold, digest pin, or assertion was touched.

## Item 3 — one real defect fixed; the rest is another session's live tree

Reproduced with the named verifier, `cd web && npx eslint . --max-warnings 0`.
The prompt's description was incomplete on both ends:

- It named two scratch files. There were **three** (`scratch_check_kit.js`,
  `scratch_check_motion.js`, `scratch_check_page.js`).
- It did not mention a **real source defect**: `web/src/components/ui/Kit.tsx:27`,
  rule `react-hooks/set-state-in-effect`.

**Fixed (verified).** `Kit.tsx` `AnimatedNumber` called `setDisplay(NaN)`
synchronously inside a `useEffect` purely so the render guard — which read the
`display` state mirror — would show an em-dash. The prop `value` is the actual
source of truth for "is this a number at all", so the guard reads `value` and the
`setState` disappears. Two lines, root cause, no rule disabled and no comment
suppression:

- `daemon`-free change, `web/src/components/ui/Kit.tsx:27` — `return setDisplay(NaN)` → `return`
- `web/src/components/ui/Kit.tsx:47` — guard now tests `value`, not `display`

Kit.tsx no longer appears in eslint output. `node scratch_check_kit.mjs` → exit 0.

**Scratch files: converted, not deleted.** Established by search that they are
referenced nowhere in build scripts, CI, `package.json`, or code — the only hits
repo-wide are `REPAIR_LOOP_SUPERPROMPT.md` and one self-referential usage comment.
They are nonetheless part of another session's in-flight untracked design work
(`DESIGN_BRIEF.md`, `src/app/motion.css`, `src/components/ui/`), so they were
converted to ESM rather than deleted — converting is reversible and loses nothing,
deletion is neither. `package.json` has no `"type"`, so ESM here means `.mjs`.
Created `scratch_check_kit.mjs`, `scratch_check_motion.mjs`, `scratch_check_page.mjs`.

**BLOCKER — concurrent writer.** Another session is actively editing `web/`:

| Time | Observation |
|---|---|
| 17:33 | 6 modified web files; 3 `scratch_check_*.js` |
| 17:43 | `scratch_check_page.js` **recreated** after I deleted it |
| 17:44 | `scratch_check_types.js` **created** (new, never seen at 17:33) |
| 17:44 | 13 modified web files; eslint 5 errors/14 warnings → **17 errors/22 warnings** |

A `PostToolUse` hook also reported "Another chat's dev server is running in this
folder." The new errors are in files that did not have them an hour ago
(`intel/companies/page.tsx`, `watchlist/page.tsx`, `market/unusual/page.tsx`).

Continuing to drive `web/` to green would (a) destroy another agent's in-flight
work — which the operating rules forbid — and (b) produce a green that is stale
within a minute. Stopped contending. **Resume when `web/` is quiescent**, then:

```bash
cd web && npx eslint . --max-warnings 0
```

Remaining known errors at 17:44, all in the other session's files: 3×
`no-require-imports` (`scratch_check_page.js`, `scratch_check_types.js`), 3×
`react-hooks/static-components` (`intel/companies/page.tsx` — `SortArrow`
declared inside render), 2× `react/no-unescaped-entities`
(`market/unusual/page.tsx:219`), 7× `no-explicit-any` + 2×
`set-state-in-effect` (`watchlist/page.tsx:44,140`).

## Item 7 — root cause located, fix not yet written

`daemon/internal/api/version.go:27`:

```go
"resolvable": lineage.BuildRevision() != "" && !lineage.BuildModified(),
```

Both operands are **build-time** facts. The endpoint reports `resolvable: true`
whenever a revision string was stamped into a clean build — it never re-checks
that the stamped revision still resolves in the authoritative source. A revision
that was rebased away, force-pushed over, or lives only on a deleted branch still
reports `true`. That is precisely the "trusts a build-time `resolvable: true`"
defect.

Next concrete action: verify at request time (or via a freshness-bounded cache
that preserves truth), with an explicit timeout and a failure mode that degrades
to *unknown/unresolved* rather than to a false `true`; add a test proving a
stamped-but-non-resolving revision reports unresolved while a valid one succeeds.
Must not weaken the dirty-build or publication refusals.

---

## Integrity statement

No check, threshold, assertion, refusal, quarantine, or provenance rule was
weakened, suppressed, re-pinned, or bypassed. No lint rule was disabled and no
suppression comment was added. No `SIGNALDECK_ALLOW_DIRTY_BUILD` was set. The
alphax gate, null/narration quarantine, and README `WITHHELD` rows were not
touched. Nothing was committed.
