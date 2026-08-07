# Improvement backlog

The self-improvement loop (`ops/selfimprove-loop.ps1`) consumes this file top to
bottom. One item per `## ` heading. The loop works the FIRST unchecked item each
cycle, and checks it off only when its stated verification command passes.

Order is priority. Put new work where it belongs, not at the end.

Each item needs a `verify:` line holding a shell command that exits non-zero
until the item is genuinely done. An item without a runnable verification is a
wish, not a task — the loop skips it and says so.

**Accuracy goal (set 2026-08-07).** Baseline in `ops/accuracy_baseline.json`:
1d acc 46.41% vs naive 57.46% (lift **-11.05pp**, 16,652 independent symbol-days);
1w acc 49.20% vs naive 51.41% (lift -2.21pp). Target is +10% RELATIVE at fixed
coverage — 1d 46.4%→51.1%, 1w 49.2%→54.1%. Progress: `python ops/accuracy_baseline.py --report`.

Two things this goal is NOT. It is not +10 percentage points on 1d direction: the
arcsin law puts 56% at IC≈0.21 and 60% at IC≈0.31, against a measured system IC
of ~0.05 and a hedge-fund range of 0.10-0.17, and four independent measurements
on this data put the directional ceiling at ~55%. And it is not reachable by
abstaining — every accuracy gate below checks coverage in the same command,
because dropping coverage raises accuracy for free.

---

## [ ] 1d forecasts collapse onto a single market call

The 1d cross-section is not a per-symbol forecaster. Measured 2026-08-07 over the
last 10 graded days, the fraction of the ~328-symbol cross-section called "up":

    2026-08-05  0.991      2026-08-02  0.015      2026-07-31  0.015
    2026-08-04  0.129      2026-08-01  0.012      2026-07-29  0.018

Five of ten days sit outside [0.05, 0.95] — every symbol gets the same side, so
one daily market call is published as ~328 independent forecasts and graded as
328 observations. It also lands inverted: on the days it called 1-2% up, the
market rose 63-70%, scoring 32-37%. This single defect is the whole -11.05pp
deficit; the surface is otherwise a coin flip.

`globalCalibration` already DETECTS this (`ranked=false`) and only `slog.Info`s
it. Make the detection fail closed at the publish boundary: when a day's
cross-section carries no dispersion, refuse the map and publish raw uncorrected
probabilities rather than writing a replicated market call. Refusing costs ~0.8pp
on the pooled record and is still right — those points were earned by silently
becoming the majority baseline while presenting as a per-symbol forecast, and
they cost -17pp the day the market turned.

Do NOT satisfy this by widening the band, by dropping the collapsed days from
grading, or by abstaining. The forecasts must still be issued at full coverage.

verify: `cd daemon && go test ./... -run TestRefusesCollapsedCrossSection -v 2>&1 | grep -q "^--- PASS: TestRefusesCollapsedCrossSection"`
files: `daemon/internal/ensemble/calibration.go`

## [ ] 1d cross-section stays dispersed on live data

Live confirmation of the item above, on forecasts written AFTER the guard ships.
This cannot go green tonight — it measures new forecast days as they accrue, so
expect it to stay red until the daemon has written a few clean days.

verify: `python ops/accuracy_gates.py xsection --horizon 1d`
files: `daemon/internal/ensemble/calibration.go`

## [ ] 1d accuracy beats its own naive baseline

Currently 46.41% against a folded majority baseline of 57.46% at coverage 1.0000.
Beating the baseline is the floor, not the goal — but a forecaster losing to
"always call the majority side" has a defect, not a weak edge. Coverage is
checked in the same command; an improvement bought by issuing fewer calls fails.

verify: `python ops/accuracy_gates.py beats-naive --horizon 1d`
files: `daemon/internal/ensemble/calibration.go`

## [ ] 1w accuracy beats its own naive baseline

49.20% against 51.41% at coverage 0.9977. The 1w map ships a rank-preserving
calibration whose outputs all sit below 0.5, so the directional CALL is
unanimously "down" even though the ranking discriminates. The preserved ranking
is the real win; the hard 0.5 threshold against a base rate near 0.466 is the
defect. Fix the threshold, not the ranking.

verify: `python ops/accuracy_gates.py beats-naive --horizon 1w`
files: `daemon/internal/ensemble/calibration.go`

---

## [x] Research-loop liveness: judgments missing for 2026-07-26..29

`tools/research_liveness.py` refuses to let the accuracy registry publish because
`worker_runs` narrated 48-rule grid searches on four days while
`research_loop_judgments` / `research_loop_runs` hold no rows for those days.
Either the judgments were never persisted (fix the writer in
`daemon/internal/pipeline/researchloop*.go` so a narrated search cannot commit
without its judgment rows), or the narration overstates what ran (fix the
narration). Do NOT delete the liveness check.

verify: `python tools/research_liveness.py --db data/signaldeck.db`
files: `daemon/internal/pipeline/researchloop.go`

## [x] Structural outcomes carry no naive_label

16,022 of 19,058 `regime_outcomes` rows have a NULL `naive_label`, so
`regime-outcome-runner` refuses to run and no structural kind can ever be
graded. Find the write path that produced label-less rows and make the guard
unbypassable there. Backfilling the label is only correct where the naive
baseline can be recomputed from stored bars; where it cannot, quarantine the
rows rather than inventing a label.

verify: `python -c "import sqlite3;n=sqlite3.connect('file:data/signaldeck.db?mode=ro',uri=True).execute('select count(*) from regime_outcomes where naive_label is null and ts > 1785000000').fetchone()[0];raise SystemExit(1 if n else 0)"`
files: `daemon/internal/pipeline/regimeoutcomes.go`

## [x] Project root is hardcoded to $HOME/claude code

`daemon/internal/config/config.go` builds four paths from
`filepath.Join(home, "claude code", ...)` (lines 78, 125, 158, 166). This repo
lives at `$HOME/Desktop/claude code`, so the daemon silently loaded NO `.env`:
the NVIDIA key was ignored, Alpaca keys were never found, and the security
toggles in `.env` never applied. Resolve the root by walking up from the
executable/cwd for a directory containing `signaldeck/daemon/.env`, honour a
`SIGNALDECK_ROOT` override, and keep the current path as the last fallback so
the macOS launchd deployment is unaffected.

verify: `cd daemon && go test ./internal/config/...`
files: `daemon/internal/config/config.go`

## [x] Grader digest breaks on a Windows clone

`core.autocrlf=true` stores `tools/accuracy_registry.py` as LF and checks it out
as CRLF, which changes its SHA-256 — so a fresh Windows clone gets a grader that
refuses to grade itself against the digest pinned in the prereg chain. Add a
`.gitattributes` marking the digest-pinned files `-text` so their bytes are
identical on every platform.

verify: `python -c "import hashlib,subprocess,sys;d=hashlib.sha256(open('tools/accuracy_registry.py','rb').read()).hexdigest();sys.exit(0 if b'\r\n' not in open('tools/accuracy_registry.py','rb').read() else 1)"`
files: `.gitattributes`

## [x] Prereg chain pins a stale grader digest

The chain's newest `grading-protocol` record pins the grader digest from before
the encoding fix, so `tools/accuracy_registry.py` refuses to grade. The
registrar appends the amendment automatically on a clean (non-dirty) build —
confirm that path runs, rather than appending by hand.

verify: `python tools/accuracy_registry.py --db data/signaldeck.db`
files: `daemon/internal/pipeline/prereg.go`

## [x] Silent data sources: congress, EDGAR

`congress_trades` holds 0 rows while `congress-poller` reports `ok` ("congress
mirrors unavailable", 94 dq events in 7 days). `edgar-fetcher` and
`filings-poller` report `ok` with "skipped: no EDGAR client" on every run. A
source that never delivers must not report success — make the pollers surface a
degraded status the watchdog can see.

verify: `cd daemon && go test ./internal/pipeline/... -run 'Poller|Source'`
files: `daemon/internal/pipeline/congress.go`
