# 2026-08-06 — The 8,713 disagreeing 1d labels: what they are, and why only about half are a defect

If you have just recomputed the 1d labels from `bars` and found ~8,700 rows that
disagree with `prediction_outcomes.up`, **stop before you "fix" them.** This
records what that number is made of, because the obvious repair overwrites
thousands of rows that were correct when they were written.

## The defect that is real, and is already fixed forward

`PredictionResolver.Run` froze a label as soon as a forward bar EXISTED. Its only
time check was `now < target -> skip`, and `target` is the next session's bar
stamp (ET midnight) — a bar ingest creates at the OPEN. So any resolver pass
during that session read a bar whose `Close` was the live price, wrote it in as a
close-to-close outcome, and never revisited it.

Fixed by the settlement guard in `PredictionResolver.Run`: resolve only once a
bar STRICTLY AFTER the forward bar exists. A successor bar can only appear after
the next session opens, so it settles the previous one without the code needing
to know exchange hours, half-days, DST, or crypto's 24h day.

## The measurement (read-only, whole population, 2026-08-06)

161,585 resolved 1d rows, relabeled under the resolver's own rule plus the
settlement guard:

| | rows |
|---|---|
| agree — unchanged | 152,795 (94.56%) |
| disagree | **8,713 (5.39%)** |
| forward session unsettled — left as-is | 77 |

Split by whether the label was frozen before the forward bar's 16:00 ET close:

| | rows | flips | rate |
|---|---|---|---|
| frozen MID-SESSION | 55,116 | 5,643 | **10.24%** |
| frozen AFTER the close | 108,653 | 3,070 | **2.83%** |

## The trap: `resolved_at` separates the RATE, not the MECHANISM

The tempting read is "the 5,643 are the defect, the 3,070 are something else."
That is wrong, and it is the reason this file exists.

The after-close rate of 2.83% is not noise around the defect — it is the
**ordinary bar-revision rate**. Daily closes get corrected after they are first
written (late consolidated prints, vendor corrections, re-adjustment), at a ~1%
median. That happens to mid-session rows too. Applying the 2.83% baseline to the
55,116 mid-session rows predicts ~1,557 of their flips are ordinary revision as
well. So:

- **~4,086 (47%) are attributable to the partial-bar defect**
- **~4,627 (53%) are ordinary bar revision**
- **and no query can say which individual row is which.** The two mechanisms are
  separable only in aggregate.

The diagnostics confirm the groups are the same kind of thing:

| | after-close (3,070) | mid-session (5,643) |
|---|---|---|
| median \|frozen move\| | 0.41% | 0.42% |
| median \|revision\| | 0.97% | 1.24% |
| knife-edge (frozen move < 0.20%) | 31.0% | 30.5% |
| split-shaped (revision > 5pp) | 7.9% | 5.9% |
| on a symbol with a recorded split repair | 11.2% | 7.3% |

**Splits explain almost none of it** — under 8% by revision size, ~11% by symbol.
An early draft of this analysis called the after-close group "split-driven". That
was a guess and the table above refutes it; if you see that phrasing quoted
anywhere, it is superseded by this file.

## Why nothing was rewritten

The graded record is **prequential**: each row is frozen on what was knowable at
decision time, and the registry's verdicts are computed from it. Relabeling with
today's bar vintage breaks that property for whatever it touches — defect rows
and correctly-frozen rows alike, since they cannot be told apart. A subset chosen
on `resolved_at` would restate ~1,557 correctly-frozen rows while *looking*
principled, which is worse than either extreme.

The forward fix (settlement guard) removes the defect from all future rows, which
is the part that changes outcomes. The 1d directional record is already
`emitting=false / verdict=retired` and labelled "not tradeable", so the payoff
from restating it is small against the cost of making a published record depend
on which day you recomputed it.

## If someone does decide to relabel

Two defensible options, and one that is not:

- **Leave it, disclose the rates.** (What is in force.)
- **Rewrite all 8,713 AND stamp `basis_epoch`.** That column exists on
  `prediction_outcomes` and is NULL on every row; it is exactly the
  "mark the build boundary" mechanism. An unmarked restatement is the thing this
  repo's governance exists to prevent.
- **NOT a `resolved_at`-selected subset.** It mixes ~1,557 correctly-frozen rows
  into the repair and cannot be defended as "only the defect".

The 77 forward-session-unsettled rows stay as they are: an unknown outcome is
better than an invented one, which is the same answer `UnresolvedPredictions`
already gives for symbols that stopped printing bars.

## Reproducing

Read-only, no writes. For each resolved 1d row: `base` = last 1d bar at/before
`ts`; `target` = `base.ts + 86400`; `fwd` = first bar at/after `target`, dropped
if `fwd.ts - target > 3*86400`; require a bar strictly after `fwd.ts`
(settlement); compare `sign(fwd.close/base.close - 1)` against `up`. Bucket on
`resolved_at < fwd.ts + 16h` for the mid-session split.
