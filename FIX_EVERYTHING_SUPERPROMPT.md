# FIX EVERYTHING — SignalDeck, measured state 2026-08-02

Paste this whole file as a single prompt. It is self-contained: assume the agent
receiving it knows nothing about this project.

Every number below was produced by a command, not carried over from a summary.
Where a thing is unverified it says so. Do not re-derive what is stated as
measured; do not trust what is stated as unverified.

---

## 0. THE ONE THING THAT IS ACTUALLY BROKEN

SignalDeck builds, runs, ingests, grades, and refuses dishonest output correctly.
The infrastructure is in good shape. **The product claim is not.**

| measured, two independent ways | value |
|---|---|
| directional accuracy, 1d | 50.01% (11,216 independent symbol-days, 22 days) |
| precision on issued calls | 0.4832 (132,156 graded pairs) |
| naive always-down baseline | 55.76% / 0.5280 |
| **edge vs naive** | **-5.75pp** |
| Brier skill | -0.138 |
| IC, day-clustered | -0.020, CI [-0.0404, +0.0032] — **includes zero** |
| live calibration curve | **slopes DOWNWARD**: pred 0.359 realizes 0.521; pred 0.520 realizes 0.453 |

**And the finding that kills the obvious fix:** precision does NOT rise with
conviction. Measured across the whole record:

| conviction band | issued | precision | base rate | edge |
|---|---:|---:|---:|---:|
| 0.00-0.02 | 17,856 | 0.5013 | 0.5178 | -0.0165 |
| 0.02-0.05 | 21,889 | 0.4875 | 0.5264 | -0.0390 |
| 0.05-0.10 | 16,198 | 0.4679 | 0.5164 | -0.0485 |
| 0.10-0.20 | 30,167 | 0.4699 | 0.5289 | -0.0590 |
| **0.20-0.50** | 43,266 | **0.4890** | 0.5569 | **-0.0679** |

The most confident band is the worst. Abstaining from 88% of calls still yields
0.4957 precision against a 0.5289 base rate.

**Therefore: no threshold, no abstention rule, and no recalibration of the
existing blend reaches 80% precision, or even 55%.** Anything that claims
otherwise on this model has made an error.

**And do NOT flip the sign.** Inverting every call gives 0.5168, still below the
0.5280 majority baseline, and the IC confidence interval includes zero. The
honest claim is *no measurable edge with an inverted point estimate* — not "a
reliable inverse signal". Two of three calibration bins straddle the base rate.

## 1. THE ARCHITECTURAL CAUSE

Traced in `daemon/internal/pipeline/predict.go` and `internal/ensemble`:

```
bars -> ~40 features (featureVersion v11)
     -> SIX independent legs, each emitting its own P(up):
        Pressure, Expectancy, Forecast, Sentiment, GBM, MeanRev
     -> ensemble.WeightedProbability:  raw = sum(w_i * p_i) / sum(w_i)
     -> isotonic recalibration (personal -> global)
     -> predictions.raw_prob / cal_prob
```

Three compounding problems:

1. **A weighted mean of disagreeing legs regresses to 0.5 by construction.** GBM
   at 0.75 and Pressure at 0.30 average to ~0.52. Measured 50.01% is precisely
   what that arithmetic produces.
2. **One leg is documented anti-predictive by the code itself.**
   `predict.go:501`: *"the resolved record shows its fixed-weight directional
   call is anti-predictive at 1d/1w"*. There is a lift gate, but an **unmeasured**
   leg is KEPT as fail-safe — so a cold trainer silently readmits it.
3. **Calibration cannot create information.** Fitting an isotonic map on noise
   yields well-calibrated noise, which is why Brier skill is negative: confidently
   wrong. The map inverting is a SYMPTOM of the blend carrying anti-signal, not a
   bug in the map.

## 2. WHAT IS ALREADY TRUE — DO NOT "FIX" THESE

Refusals verified CORRECT. A later pass that removes them is a regression.

- **README rows reading `WITHHELD (provenance unresolvable)`.** `distinct_days`
  is 8 against a floor of 10, and the rows were written by `(unstamped)` and
  `+dirty` builds. This heals ONLY by accruing days from an attributable build.
  Never re-pin `graderSha256` or widen a floor to make it publish.
- **The daemon's dirty-build refusal.** Fired twice and was right both times.
  Never set `SIGNALDECK_ALLOW_DIRTY_BUILD=1` against the production database.
- **The `alphax` gate.** OOS lift -0.0072, correctly stored and never blended.
- **No "persistently negative IC" alarm** without first establishing the leg's
  weight sign. A factor can carry a negative weight by design, so flagging
  meanrev's stable -0.1616 would be a false alarm.
- **The null quarantine and the narration quarantine.** Both are enumerated,
  digest-covered and chain-recorded. They exclude specific historical rows from
  refusal; they do not weaken any check.

## 3. CURRENT SUPERVISION STATE — verified just now

The prior handoff named "nothing supervises anything on this Windows host" as the
root cause. Partially closed since:

| | state |
|---|---|
| Windows Scheduled Task `SignalDeck Daemon` | **Ready**, at-logon, restart every 5 min |
| daemon `/api/health` :8322 | **200** |
| tickstreamd :8321 | **200** |
| `snapshots_1s` | filling |
| backup to separate hardware | **NOT DONE** — still same disk |
| tickstreamd supervision | **NOT DONE** — running by hand |
| backup supervision | **NOT DONE** — ad hoc |

## 4. THE WORK, IN PRIORITY ORDER

Each item states its own verification. An item without one is a wish.

### P0 — irreversible risk

**A. Offsite backup to separate hardware.** Every backup, including the good
2026-08-02 one, sits on the same disk as the database. A single drive failure
loses the database AND every backup.
```
SIGNALDECK_OFFSITE_DIR=<external drive> bash ops/signaldeck-backup-offline.sh
```
verify: `/api/quality.ops` reports `offsiteSameVolume: false`.

### P1 — closes on a specific clock, cannot be done later

**B. Live equity tick verification.** Monday 09:30 ET. Confirm parsed equity
bars land in `bars` with fresh timestamps. Audited on a Sunday twice; equities
were closed both times.
verify: rows in `bars` for a streamed stock symbol with `ts` inside the session.

**C. Socket reconnection with no duplicate subscriptions.** Kill the live
websocket during market hours; confirm it redials and `resync()` does not
double-subscribe.
verify: one subscription per symbol in the stream state after recovery.

### P2 — the product claim

**D. Do NOT tune the existing blend. Rebuild the decision rule.**
The measurement in section 0 forecloses thresholding. What is not yet tested:

- **Leg agreement instead of leg averaging.** Issue a call only when N of 6 legs
  agree in direction AND each exceeds its own conviction floor; abstain otherwise.
  This is a different function, not a threshold on the same number.
- **Per-leg standalone precision.** Nobody has published each leg's OWN hit rate
  against the majority baseline. One leg may carry signal that averaging destroys.
  Measure all six separately before combining anything.
- **Drop, do not down-weight, any leg whose standalone edge is negative.**

verify: precision, issued-subset base rate, abstention rate and day-clustered
interval for each candidate rule, on a sealed era, via `tools/accuracy_registry.py`.

**E. F-5 — why did the calibration map invert?** The highest-value research
question open. It is a symptom of section 1; treat it as diagnosis, not repair.
Do not flip the sign.

### P3 — correctness

**F. D-1 — `resolvable: true` is never re-checked.** Stamped at build time;
nothing confirms the commit still resolves, so a force-push leaves rows stamped
to a commit nobody can look up.
verify: version endpoint re-verifies the revision resolves.

**G. Supervise tickstreamd and the backup** the same way the daemon now is.
verify: both survive a reboot without a human.

## 5. METHOD — non-negotiable

- **Fix the root cause, never the symptom.** Grep every caller before editing.
- **Never weaken, delete or special-case a check to make a number look better.**
  This repository's value is that its checks refuse dishonest results. A green
  obtained by softening a guard converts a measurement system into a marketing
  system. It is the worst available action.
- **Every claim carries its base rate, its abstention rate, and a
  multiplicity-corrected interval.** Accuracy alone is never evidence.
- **Count independent observations, not rows.** One (symbol, UTC day) is one
  observation; the measured design effect is ~6.99.
- **Rebuild the binary after every commit** or rows become unattributable:
  ```
  go build -ldflags "-X github.com/nyaungnicholas-wq/signaldeck/internal/lineage.ldflagsRev=$(git rev-parse HEAD)" -o ../bin/signaldeckd.exe ./cmd/signaldeckd
  ```
- **Re-derive every worker result.** Worker output is a draft; run it or run the
  tests before accepting it.

## 6. DEFINITION OF DONE

- [ ] A backup exists on hardware that is not the database's disk
- [ ] Live equity ticks observed landing during a session
- [ ] A killed socket recovers with no duplicate subscription
- [ ] Each of the six legs has a published standalone precision vs its base rate
- [ ] Any rule claiming edge shows precision, base rate, abstention and a
      day-clustered corrected interval on a sealed era
- [ ] tickstreamd and the backup survive a reboot unattended
- [ ] `resolvable` is re-verified rather than stamped once
- [ ] Every gate green and `ops/pre-publish-scan.sh` reads SAFE TO PUBLISH

## 7. THE HONEST FRAME

The measurement layer works. It has correctly refused to publish a verdict it
cannot support, and correctly reported that the flagship predictor has no edge.
That is the system functioning, not failing.

What does not yet exist is a predictor with measurable edge. Do not confuse
fixing the infrastructure with fixing the claim. An airtight negative result is
worth more than a positive one that is not, because only one of the two can be
traded.
