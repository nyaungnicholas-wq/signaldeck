# Adversarial re-audit — 2026-08-03

Scope: refute, not validate. Findings below were reproduced against the running
system (daemon :8322 uptime ~70m, web :8323) unless marked otherwise. Report
only — no code was changed.

Prior work read first so nothing already on the record is re-reported as new:
`REMEDIATION_2026-08-03.md`, `SYSTEM_CHECK_2026-08-02_REAUDIT.md`, `README.md`.
The six items in that doc's "Still open" list are **not** repeated here.

---

## F-1 (HIGH) — `/accuracy` renders a REFUSED registry as a successful one

**Where:** `web/src/app/accuracy/page.tsx:64-71` (the `Registry` type),
`:267-269` (`AccuracyPage`), `:333-340` (the only unavailable branch),
`:386-392` (the footer).

**What breaks.** `ops/accuracy-registry.sh` writes a *refusal-shaped* JSON when
the grader will not run: `{status:"REFUSED", rows:[], refusal_reason,
refusal_stderr, stale_last_registry:{…}}` — top level carries **no**
`min_independent_n`, `survivorship_epoch`, `null_policy`, or `calibration`
(those move inside `stale_last_registry`). The page's `Registry` type has no
`status` field and no refusal handling. `loadRegistry()` parses the file
successfully, so `reg` is non-null and the `!reg` "registry unavailable" panel
at `:333` never fires. `rows: []` means zero directional rows, no calibration
panel, no structural table. The footer at `:386` then prints `reg.generated`
verbatim.

The README does the honest thing on this exact state — it *removes* the tables
and prints "GRADING REFUSED — no accuracy numbers are published". The README
then directs the reader to this page ("in-app at `/accuracy`", README:25). The
refusal contract is enforced on one surface and absent on the other.

**Reproduced live** (`curl http://localhost:8323/accuracy`, 23,836 bytes):

- `"REFUSED"` — **absent**. `"refus"` — **absent**. The page never says the
  grader refused.
- Footer renders: `Regenerated 2026-08-02T22:33:41 · minimum  independent
  observations for any verdict · survivorship epoch  (earlier rows were
  graded against a survivor-seeded universe and are excluded)` — the two
  missing values render as blank gaps, because `reg.min_independent_n` and
  `reg.survivorship_epoch` are `undefined`.
- The only numbers on the page are the hardcoded `FLAGSHIP_RETIREMENT` table.

**Wrong-answer scenario.** A visitor opens `/accuracy` today. They see a
current-looking "Regenerated 2026-08-02T22:33:41" stamp, a page that states it
is "regraded daily against the naive baseline", and no live rows. The correct
reading — *the grader refused, its inputs failed a liveness check, and the
previously published numbers were withdrawn* — is unavailable anywhere on the
page. The available reading — *nothing is currently being graded* — is a
materially different claim. A blank grading surface and a withdrawn grading
surface must not render identically on a page whose thesis is that a refusal is
itself the finding.

**Secondary:** `:344` passes `minN={reg?.min_independent_n ?? 30}` — under
refusal the real evidence floor is unreadable and the literal `30` silently
stands in for it, so any row that *did* survive would be gated against a
fabricated threshold.

---

## F-2 (HIGH) — the recommendation API serves a stale price under a current timestamp

**Where:** `daemon/internal/api/desk.go:180-182` (`/api/recommendation`) and
`:325-327` (`/api/recommendation/top`). Structural cause:
`daemon/internal/recommendation/recommendation.go:52-53` — `Inputs` has
`HasPrice bool` and `Price float64` and **no price timestamp field**, so the
recommendation engine cannot gate on price age even in principle.

**What breaks.** Every other leg in `recInputs` is age- or availability-gated:
the prediction carries `PredAgeSec: time.Now().Unix() - p.PredTs` into
conviction (`:164`), fair value refuses without EPS, expectancy/regime/rank all
degrade to absent. The price leg does not:

```go
if bars, err := d.St.LastBars(ctx, s.ID, md.TF1d, 1); err == nil && len(bars) > 0 {
    in.HasPrice, in.Price = true, bars[0].Close
}
```

`LastBars` returns the newest daily bar regardless of age. The response is then
stamped `AsOf: time.Now().Unix()` (`:139`) with no staleness field.

**Reproduced live.** The daemon's own DQ feed flags
`MVO — last daily bar 10d old (daily-only universe)` every ~50 minutes
(`/api/quality`, most recent at 08-03 07:46 UTC). MVO is `active=1`,
`delisted_at IS NULL`. Querying the recommendation surface for it:

```
GET /api/recommendation?symbol=MVO&market=stocks
  available          = True
  asOf               = 2026-08-03 07:48 UTC   ← now
  hasPrice           = True
  currentPrice       = 0.5955                 ← a 10-day-old close
  decision           = Watch
```

Nothing in the payload marks the price stale, and the Fact Verification Agent —
the role whose stated job is to say what is and is not measured — reports on
accuracy, fair value and scenario provenance but says nothing about the price.

**Wrong-answer scenario.** A halted, suspended, or feed-dropped name is quoted
at a days-old close as its current price. For any such name that also has EDGAR
EPS on file, `ExpectedReturnPct = (fairValue − price)/price × 100`
(`recommendation.go:105`) is computed against that stale denominator and
published as a percentage. MVO escapes the second half only because it has no
EPS.

**Scope, measured, not assumed.** A read-only query of the live DB: 322 active
stocks; **1** whose newest 1d bar is >5 days old (MVO); 0 beyond 25 days. So
this is one symbol today, not a fleet-wide corruption — but it has been that one
symbol for 10+ days, and the gate that would catch the next one does not exist.
(The `delisting_check_skipped` event citing "719 of 1773 stocks" refers to the
wider candidate universe, not the served set; the guard refusing to mark those
delisted is correct and is not a finding.)

---

## F-3 (MEDIUM) — `/accuracy` hardcodes a verdict table its own header says cannot exist

**Where:** `web/src/app/accuracy/page.tsx:7-10` vs `:76-83`.

The file header states the design invariant:

> "reads `data/accuracy_registry.json` straight from disk … so the page can
> never disagree with the file the daily grade wrote — **there is no second copy
> of the verdict logic here to drift**."

Sixty lines later:

```js
const FLAGSHIP_RETIREMENT = {
  date: "2026-07-24",
  rows: [
    { name: "directional-ensemble (1d)", acc: "48.1%", baseline: "54.6%", n: "13,058", skill: "−6.5pp" },
    …
```

**Wrong-answer scenario.** These are the only numbers the page currently
renders (see F-1). They are a frozen 2026-07-24 snapshot with no provenance
link, no regrade path, and no consistency check against the registry. If the
full-record grade is ever recomputed — a survivorship-epoch change, a resolution
fix, a dedup change — the registry moves and this table does not, and the page
publishes two different accuracies for the same predictor with the stale one
rendered largest and in red. Being a "permanent fact" is an argument for pinning
it in the registry, not for a second copy in the renderer.

---

## F-4 (MEDIUM) — WAL checkpoint starvation; the likely cause of the standing grading refusal

**Where:** live `dq_events`, kind `wal_checkpoint_busy`. Observed today:

```
08-02 23:57  WAL 706.5MB and growing: TRUNCATE blocked by active readers (68730/143837 frames moved, quiesced=true)
08-03 04:57  WAL 280.8MB and growing: TRUNCATE blocked by active readers (49148/60624 frames moved, quiesced=true)
08-03 06:37  WAL 389.9MB and growing: TRUNCATE blocked by active readers  (3941/99013 frames moved, quiesced=true)
```

accompanied by `quiesce_stall :: fleet drain 30.05s (24 in flight of 97
workers, timedOut=true)` on every attempt. The fleet's 30-second drain window
never clears 97 workers' readers, so the checkpoint gives up every pass and the
WAL only shrinks by whatever partial frames it moved.

**Why it matters beyond disk.** The registry's own recorded refusal is:

```
"refusal_stderr": "research-loop liveness: query failed: disk I/O error"
"refused_since":  "2026-08-02T22:33:41"
```

That is 24 minutes before the 23:57 event above recorded a 706 MB WAL. A
read against a WAL that large, under a checkpoint fighting 97 readers, is a
plausible source of a transient `disk I/O error`. **I did not prove causation** —
the DB passes `PRAGMA quick_check` cleanly now and I did not reproduce the I/O
error. Stated as the leading hypothesis, not a conclusion.

The consequence is the part that is certain: one transient read failure in a
liveness *precondition* withdraws every published accuracy number until the next
successful grade, and (F-5) the next successful grade has not happened.

---

## F-5 (MEDIUM) — the grader has never run on this machine; the refusal has been frozen 33 hours

**Where:** Windows Task Scheduler, tasks generated by the new
`ops/install-windows-tasks.ps1`.

```
SignalDeck Accuracy    state=Ready  last=11/30/1999  result=267011 (0x41303, "has not yet run")  next=2026-08-03 14:05
SignalDeck Bias        state=Ready  last=11/30/1999  result=267011                               next=2026-08-03 02:40
SignalDeck Daily-Refresh  …         last=11/30/1999  result=267011                               next=2026-08-03 13:15
SignalDeck Market-Open/Close/Cleanup/Restore — same, all never run
SignalDeck Research-Liveness  last=2026-08-02 23:34  result=0
SignalDeck Eighty Loop        last=2026-08-03 00:36  result=2147946720 (0x800710E0, "operator or administrator has refused the request")
```

`ops/accuracy-registry.sh:14-15` already names this exact hazard —

> "the grader could not run AT ALL, and the README kept serving the refusal it
> had recorded on 2026-07-29 as though it were current. A stale refusal is
> worse…"

— and the system is in that state again, now for 33 hours, with no alarm. The
registry's `last_successful_grade_age` field exists but nothing watches it: the
freshness of the *grader itself* is the one liveness signal with no watchdog.
`ops/lib-portable.sh`'s `sd_notify` guarantees an alert is *written* when a
refusal happens; it cannot fire for a refusal that is simply never revisited.

The tasks are registered and scheduled, so the first `Accuracy` run should occur
at 14:05 today. Verify it and check `result=0` — the 0x800710E0 on Eighty Loop
shows registration alone does not imply execution.

**Related, smaller:** `worker_stale :: worker=hud-sync last successful run
never` still fires hourly (00:26, 01:26, …, 05:26 today). Remediation item 5
moved hud-sync to `ErrDegraded` with 1m→30m backoff so an absent external
process stops counting as a SignalDeck failure; the *worker-staleness* watchdog
was not taught the same thing, so the noise moved channels rather than stopping.

---

## Areas examined where I found no defect

Stated explicitly rather than padded into findings.

- **`signalbt` backtest engine** (`daemon/internal/signalbt/signalbt.go:189-249`).
  No lookahead. `Backtest` consumes `Signal` and `FwdByLag` as given, has no
  access to bars, and cannot recompute a signal from future data.
  `nolookahead_test.go` asserts the property at the boundary in both directions
  (a flat signal cannot earn the move; mutating a later observation cannot move
  an earlier equity mark). The result is labeled `Live:false`,
  `TrackLabel:"backtested — not live"` and the equity block is withheld as JSON
  `null` (not `0`) when the accounting fails.

- **Prediction resolution alignment** (`daemon/internal/pipeline/predict.go:714-754`).
  Correctly as-of: `base = BarAtOrBefore(ts)`, `fwd = BarAtOrAfter(base.Ts +
  horizon)`, with a `fwd.Ts - target > 3*horizon` reject so a resumed feed
  cannot resolve an ancient prediction against an unrelated bar. The benchmark
  rows (`<horizon>#pm`) resolve through the identical path.

- **Training-set construction** (`daemon/internal/store/features.go:96-105`).
  `LabeledFeatures` joins `features` to `prediction_outcomes` on identical
  `(symbol_id, horizon, ts)` and admits only rows with `resolved_at`, `up`, and
  `fwd_return` all non-NULL, so an open prediction cannot train the map later
  applied to it. `InsertFeatures:35-37` refuses an empty vector outright rather
  than persisting a zero row.

- **Calibration-map coordinate error** (`store/predict.go:157-174`). Already
  correct and documented: `ResolvedRawPredictionPairs` fits on `raw_prob`,
  `ResolvedPredictionPairs` grades on the published `prob`. Fitting a map on its
  own previous output is explicitly prevented.

- **GBM self-reference** (`daemon/internal/pipeline/gbmtrain.go:41-95`).
  `pred_raw`/`pred_cal`/`gbm_prob`/`meanrev_prob`/`alphax_prob` and the four A7
  keys are banned from training, including retroactively for pre-deletion rows.
  The presence-indicator derivation (`:115-124`) prevents a tree from learning a
  data-provider outage as a market state — a real leakage class, handled.

- **Mean-reversion grading** (`daemon/internal/meanrev/meanrev.go:182-238`).
  Graded net of round-trip cost against the best no-skill *constant* strategy,
  not against 0.5; a sample too small to trade counts as a loss for signal and
  benchmark alike so it cannot inflate lift; `Lift <= 0` drops the leg rather
  than down-weighting it.

- **Fair-value heuristic** (`desk.go:27-30`, `recommendation/agents.go:62-88`).
  `peerPE = 20.0` is hardcoded but is a labeled heuristic at every surfacing
  point, is suppressed entirely without EPS, and names non-positive EPS
  explicitly. Not a fixture standing in for real data.

- **`retiredFromRegistry` fail-open** (`pipeline/modelhealth.go:303-347`). An
  unreadable or refused registry yields an empty retire-flag map, which is the
  permissive outcome — but this is deliberate, documented, and backstopped by
  `Grade`'s own skill gate reading the store's record directly. Not a defect as
  written; noted only because F-1 and F-5 mean that fallback is load-bearing
  right now.

- **`srchealth` contradictory freshness message** ("newest row *no rows* old …
  in the future; check clock skew"). Visible in live `dq_events` at 00:56 and
  03:56 today, but **already fixed in source** —
  `daemon/internal/srchealth/srchealth.go:182-197` documents and repairs exactly
  this contradiction. The events predate the 06:38 daemon restart. Not a finding.

- **Delisting guard** (`delisting_check_skipped`). Refusing to mark 719
  non-printing names delisted when only 59% of the candidate universe printed a
  bar — treating it as an ingestion fault instead — is the conservative and
  correct posture. Not a finding.

## Not re-reported

`REMEDIATION_2026-08-03.md` "Still open" items 1–7 (no off-machine backup;
`signaldeck-refresh.sh` macOS-bound; 3 symbols lacking a naive baseline; the
sub-null directional ensemble; the two contradictory ICs +0.0497 vs −0.020; the
non-existent `/api/accuracy` route; the running `+dirty` build) are on the
record already. I did not independently re-derive the IC contradiction and make
no new claim about it.
