# Gate 0 — STOP. The plan was aimed at a dead configuration.

**Session:** forecast repair, 2026-08-08.
**Verdict:** Gate 0 tripped its own stop condition. Gates 1–4 are NOT started.
**Holdout SHA:** `c1f6c8af721725e5935a8f7e29bb7eebb9115dcaf424c7d773daa0607fdf8e21`
**Reproduce:** `python tools/holdout_grade.py --start 2026-08-05 --end 2026-08-06`

---

## What Gate 0 found

The forecaster's **cross-section collapsed** for 14 of the last 28 trading days.
On a collapsed day the model emits a handful of distinct probabilities across
the whole universe — every symbol receives essentially the same forecast:

| day | symbols | distinct probs | ratio |
|---|---|---|---|
| 2026-07-27 | 330 | **6** | 0.018 |
| 2026-07-31 | 328 | **6** | 0.018 |
| 2026-08-03 | 328 | **6** | 0.018 |
| 2026-08-05 | 327 | 179 | 0.547 (healthy) |
| 2026-08-06 | 322 | 174 | 0.540 (healthy) |

Full listing: `go run ./cmd/forecastmon --days 30`.

On those days the record is **one market-wide call repeated per symbol**, while
`n` reads as ~328 independent trials per day. The measured day-clustered design
effect on 2026-07-27..08-04 is **27.9** — 2,626 rows carry the information of
**94** observations.

## Why that invalidates the session's premise

The per-bucket table that motivated this session was computed over a window
pooling healthy and collapsed days. Restricted to clean windows, its central
claim does not survive:

| window | `<30%` bucket n | realized | conclusion |
|---|---|---|---|
| pooled (what was reported) | 3,020 | 45.5% | "informative, −11.9pp vs base" |
| collapsed 07-27..08-04 | **2** | 0.0% | no content |
| post-collapse 08-05..08-06 | **11** | 54.5% | **sign reverses**, +15.4pp |

The "strong-down bucket has edge" finding was an artifact of the collapse. Gate 2
was to be built on it. It has no support.

## The frozen holdout, in full

Post-collapse rows only (`ts >= 2026-08-05`), deduplicated to one row per symbol
per trading day:

```
n            649 symbol-days over 2 trading days
up rate      39.14%    -> baseline 60.86%  (always DOWN)
accuracy     44.07%       LIFT -16.80pp
Brier        0.2653       baseline 0.2382   skill -0.114
log loss     0.7280       ECE 0.1527
```

<!-- SUPERSEDED-SNAPSHOT — the figures in the table below are this holdout's
     own calibration buckets over 2 trading days (2026-08-05..06), not the live
     record. Its `[29.8%, 46.5%]` interval bound collides by coincidence with a
     directional-ensemble null figure from the 2026-08-08 regrade; the literal
     gate cannot tell a bucket's CI bound from a published accuracy, so the
     block is labelled. The marker exempts only the table beneath it. -->

| bucket | n | said | actual | 95% CI | vs base |
|---|---|---|---|---|---|
| <30% | 11 | 24.4% | 54.5% | [28.0%, 78.7%] | +15.4pp |
| 30-45% | 102 | 40.2% | 49.0% | [39.5%, 58.6%] | +9.9pp |
| 45-55% | 391 | 50.2% | 36.8% | [32.2%, 41.7%] | −2.3pp |
| 55-70% | 127 | 58.6% | 37.8% | [29.8%, 46.5%] | −1.3pp |
| >=70% | 18 | 79.4% | 33.3% | [16.3%, 56.3%] | −5.8pp |

**Two things make this unusable as a holdout.**

1. **Two trading days.** The accuracy registry's own floor is ten. 60% of the
   rows sit in the 45–55% coin-flip band; the two tail buckets hold 11 and 18
   rows.

2. **The baseline flipped direction.** It was *always-UP* at 52.73% on the
   previously reported window and is *always-DOWN* at 60.86% here. Two days
   cannot distinguish a regime from a coin.

The design effect cannot be measured from two clusters, so the ±4pp interval
above is almost certainly far too narrow:

| assumed design effect | effective n | 95% CI | excludes baseline? |
|---|---|---|---|
| 1 (naive) | 649 | [40.3%, 47.9%] | yes |
| 27.9 (adjacent window) | 23 | [25.6%, 63.2%] | **no** |

At the clustering measured on the immediately adjacent window, the holdout
cannot even exclude the baseline.

## What shipped anyway

Gate 2 requires a runtime monitor **either way**. That is unconditional and it is
the one thing here that pays regardless of what the forecaster turns out to be,
because every failure in this system's history was silent, not wrong.

- `internal/forecastmon` — collapse + inversion detector. Thresholds calibrated
  from the real record (healthy days 0.42–0.55, collapsed 0.018–0.040; floor
  0.15). Inversion is measured against the **base rate**, not the claim, so
  overconfidence-that-still-ranks is not flagged while negative information is.
  Withholds rather than passes below 10 distinct days.
- Registered as the `forecast-monitor` worker (24h). It returns an **error** when
  a check trips, so `/api/health` reports the daemon degraded within a day. That
  is the delivery mechanism — it needs no alert transport, and this machine has
  none configured.
- `cmd/forecastmon` — run the same check on demand; exit 1 when it trips.
- `tools/holdout_grade.py` — SHA-pinned, deduplicated, day-clustered grading with
  Wilson intervals, Brier skill, log loss and ECE. Prints `WITHHELD` under ten
  days rather than a verdict.

It never adjusts, clamps or flips a forecast. Inversion is presumed a **bug in
the data path** until traced.

## What is needed before Gate 1

Roughly **ten clean trading days** post-collapse. At the current cadence that is
~2026-08-19, and only if no further collapse occurs — which the monitor will now
say out loud.

Until then no directional product should be published. The honest output is
**"no directional product yet."**
