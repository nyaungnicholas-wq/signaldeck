# REPAIR LOOP — SignalDeck, specific work queue, measured 2026-08-02

Paste this whole file as a single prompt. Self-contained: assume no knowledge of
this project. Every number was produced by a command, not carried over.

You are a worker in a loop. **Do ONE item per cycle**, in the order given. Each
item names the exact file, the exact defect, and the exact command that decides
whether you fixed it. Do not move on until that command exits zero, and do not
skip ahead — the order is by irreversibility, not by interest.

---

## THE RULE THAT OVERRIDES EVERYTHING

**Never weaken, delete, loosen or special-case a check, threshold, assertion or
refusal to make a number look better.** This repository's entire value is that
its checks refuse dishonest results. A green light obtained by softening a guard
converts a measurement system into a marketing system, and it is the single worst
action available to you.

Specifically, these refusals are CORRECT and a "fix" to them is a regression:

- README rows reading `WITHHELD (provenance unresolvable)` — `distinct_days` is 8
  against a floor of 10, rows written by `(unstamped)` and `+dirty` builds. This
  heals only by accruing days from an attributable build. Never re-pin
  `graderSha256`, never widen a floor.
- The daemon's dirty-build refusal. Never set `SIGNALDECK_ALLOW_DIRTY_BUILD=1`
  against the production database.
- The `alphax` gate (OOS lift −0.0072, correctly never blended).
- The null quarantine and the narration quarantine — both enumerated,
  digest-covered, chain-recorded.

---

## P0 — IRREVERSIBLE RISK. DO THIS FIRST.

### Item 1 — Backups sit on the database's own disk

Every backup, including the verified-good 2026-08-02 one, is on the same physical
drive as `data/signaldeck.db`. One drive failure loses the database AND every
backup of it. This is the largest remaining risk in the system.

Fix: write a backup to separate hardware.
```
SIGNALDECK_OFFSITE_DIR=<external drive path> bash ops/signaldeck-backup-offline.sh
```
verify: `/api/quality.ops` reports `offsiteSameVolume: false`

If no external drive is attached, STOP and report that. Do not fake it by writing
to another folder on the same volume — the flag exists to catch exactly that.

---

## P1 — GREEN THE GATES

Three gates are red. Fix them in this order.

### Item 2 — `test_digest_helper_hashes_file_contents` fails

```
AssertionError: '01d6bcdfe1451b6f2c84185478f9c8f3c405ef3ae845a11a937d865eacf2a724' != ''
```
in `ProtocolDocumentGateTest`, `tools/test_accuracy_registry.py`. A digest helper
is returning an empty string where it must return a SHA-256 of file contents.

This is a PROVENANCE check. An empty digest means "this file hashes to nothing",
which would let an unverified document pass a gate designed to pin it. Fix the
helper so it hashes the file; do NOT relax the assertion.

verify: `cd tools && python -m unittest test_accuracy_registry`

### Item 3 — eslint fails on two scratch files

`web/scratch_check_kit.js` and `web/scratch_check_motion.js` use `require()`,
which `@typescript-eslint/no-require-imports` forbids.

These are scratch files. Decide deliberately: if they are throwaway, delete them
or add them to `.gitignore`; if they are kept, convert to ESM `import`. Do not
add an eslint-disable comment — that hides the rule rather than satisfying it.

verify: `cd web && npx eslint . --max-warnings 0`

### Item 4 — pre-publish scan blocked by untracked files

`ops/pre-publish-scan.sh` refuses while untracked paths exist, because a file in
the working tree that is absent from a clone means the published system is not
the audited one. Resolve item 3 first; then track or ignore whatever remains.

verify: `bash ops/pre-publish-scan.sh` reads `SAFE TO PUBLISH`

---

## P2 — CLOSES ONLY DURING MARKET HOURS

These cannot be done at any other time. Both need a live session,
**Monday–Friday 09:30–16:00 ET**.

### Item 5 — Live equity ticks landing in the DB

Never verified; every audit so far ran when equities were closed. Crypto ingest
is confirmed working (7 symbols, bars arriving each minute via Kraken REST); the
Alpaca IEX websocket authenticates and holds a session, but no equity bar has
ever been observed arriving.

verify: rows in `bars` for a streamed stock symbol with `ts` inside the current
session, newer than 5 minutes.

### Item 6 — Socket reconnection without duplicate subscriptions

Kill the live websocket during market hours and confirm it redials and that
`resync()` in `daemon/internal/ingest/alpaca/streamer.go` does not double-
subscribe. The diff logic exists and is unit-tested; it has never been exercised
against a real disconnect.

verify: after a forced disconnect, exactly one subscription per symbol in the
stream state, and bars resume.

---

## P3 — CORRECTNESS

### Item 7 — `resolvable: true` is stamped once, never re-checked

The flag is written at build time and nothing confirms the commit still resolves.
A rewrite or force-push leaves rows stamped to a commit nobody can look up, while
the API still claims the provenance is resolvable.

Fix: have the version endpoint re-verify that the revision resolves, rather than
trusting the build-time stamp.

verify: a test that fails when the stamped revision does not resolve.

### Item 8 — Supervise tickstreamd and the backup

The daemon now has a Windows Scheduled Task (`SignalDeck Daemon`, at-logon,
restart every 5 min). `tickstreamd` and the backup still run by hand, which is
why provenance gaps appear: rows get written by whatever build someone launched.

verify: both survive a reboot with no human action.

---

## P4 — THE RESEARCH DIRECTION. READ THIS BEFORE PROPOSING ANY MODEL WORK.

Do NOT try to repair the six ensemble legs. They were each graded alone on
132,156 joined pairs across 29 days, and the result forecloses that path:

| leg | present | precision | base rate | edge |
|---|---:|---:|---:|---:|
| Pressure | 132,156 | 0.4801 | 0.5280 | −0.0479 |
| Expectancy | 126,917 | 0.4955 | 0.5238 | −0.0282 |
| Forecast | 123,335 | 0.5150 | 0.5228 | −0.0078 |
| Sentiment | 11,233 | 0.4263 | 0.5011 | −0.0748 |
| GBM | 37,097 | 0.4819 | 0.5413 | −0.0593 |
| MeanRev | 38,445 | 0.5941 | 0.5369 | +0.0572 |

Five of six are worse than their own base rate. MeanRev's apparent edge is not
real: its day-clustered interval is [0.5246, 0.6637] against a base of 0.5369 —
the lower bound is BELOW the number it must beat — and split by call direction
its edge is exactly +0.0000 in every band, because within a same-direction subset
precision IS the base rate. The aggregate figure arises only from issuing UP
calls on up-heavy days and DOWN calls on down-heavy days. That is unskilled
classification: learning the class proportion, not the features.

Also measured and closed: precision does not rise with conviction. At 88%
abstention the blend still scores 0.4957 against a 0.5289 base rate. **No
threshold, no abstention rule and no recalibration of this blend reaches 80%, or
even 55%.**

And do not flip the sign. Inversion gives 0.5168, still under the 0.5280
majority baseline, with an IC confidence interval that includes zero.

**The productive direction is a DIFFERENT TARGET, not a repaired ensemble.**
Next-day direction on liquid equities is the most efficiently arbitraged
prediction in the space — published out-of-sample consensus is 54–58%, and a
controlled 918-experiment study found a mean of 50.08%. This system sits at
49.75%. Nothing in these six legs is going to clear that.

Test instead, one hypothesis per cycle, each pre-registered before grading:

1. **Realised volatility** — a non-directional quantity. Volatility clusters, so
   it is genuinely more predictable than direction. Beat the persistence null
   ("tomorrow's vol equals today's"), not 50%.
2. **Cross-sectional ranking** — predict which symbols out-perform the same-day
   universe median, rather than whether any one goes up. Removes the market
   factor that dominates directional error.
3. **A longer horizon** — 5d or 21d, where slower-diffusing information has time
   to be incorporated.
4. **A conditional subset** — a regime, a liquidity band, an event window where a
   mechanism gives a reason for edge to survive.

For any of these, the acceptance bar is unchanged: precision, the base rate of
the issued subset, the abstention rate, and a day-clustered
multiplicity-corrected interval whose lower bound clears the base rate — on a
sealed era, reproducible from committed code and data.

---

## METHOD — applies to every item above

- **Fix the root cause, not the symptom.** Grep every caller before editing.
- **A change is done only when its verify command exits zero.** Model confidence
  is not evidence.
- **Count independent observations, not rows.** One (symbol, UTC day) is one
  observation; the measured design effect is ~6.99, so 11,388 rows are 1,629
  observations.
- **Rebuild after every commit** or rows become unattributable:
  ```
  go build -ldflags "-X github.com/nyaungnicholas-wq/signaldeck/internal/lineage.ldflagsRev=$(git rev-parse HEAD)" -o ../bin/signaldeckd.exe ./cmd/signaldeckd
  ```
- **Never `git add -A`.** Another agent works in this tree; stage only the files
  you changed.
- **Report a blocked item as blocked.** An item you cannot finish honestly is
  worth more reported than faked.

## DEFINITION OF DONE

- [ ] A backup exists on hardware that is not the database's disk
- [ ] All eight gates green, `pre-publish-scan` reads SAFE TO PUBLISH
- [ ] Live equity ticks observed landing during a session
- [ ] A killed socket recovers with no duplicate subscription
- [ ] `resolvable` re-verified rather than stamped once
- [ ] tickstreamd and the backup survive a reboot unattended
- [ ] Any new edge claim carries precision, base rate, abstention rate and a
      day-clustered corrected interval on a sealed era
