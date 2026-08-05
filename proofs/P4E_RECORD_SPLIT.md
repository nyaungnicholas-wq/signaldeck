# P4E — Splitting the paper record at 2026-08-04: proof artifact

**Date:** 2026-08-04 · **Phase:** P4E
**Cause:** `proofs/P4D_BARRIER_EXITS.md` — triple-barrier exits changed what the
paper book *is*.

## Why a split was required

P4D replaced open-ended holds with a triple barrier. On `flagship-1d` the 1-bar
horizon makes the probability-flip exit unreachable, so the book went from
holding a position until the model changed its mind to opening at one bar's open
and closing at the next.

That is not a parameter change. Any return, Sharpe, drawdown or win rate
averaged across 2026-08-04 describes **a strategy nobody ran**.

## The decision taken

Two choices were open and both were the user's to make. Recorded here because
the numbers depend on them:

| Question | Chosen |
|---|---|
| What happens to the 21 positions open at the boundary? | **Carry the book into epoch 2** — capital and positions continuous, nothing closed out or reset |
| How strictly is the split enforced? | **Hard** — the risk inputs are scoped, not just the reporting |

**The book is continuous; the record is not.** Because the capital did not
restart, the boundary is *not* a new strategy id — it is a measurement boundary
over one continuous book. Inventing a fresh book would have made the chart
tidier by asserting a fact that is untrue.

## The boundary, as applied

```
$ sdmaint paper-epochs -db data/signaldeck.db

flagship-1d — 2 epoch(s)
  epoch 1  flip-exit        inception → 2026-08-04
            22 fill(s), 43 equity mark(s)
  epoch 2  triple-barrier   2026-08-04 → open
            0 fill(s), 3 equity mark(s)

flagship-1w — 2 epoch(s)
  epoch 1  flip-exit        inception → 2026-08-04
            23 fill(s), 43 equity mark(s)
  epoch 2  triple-barrier   2026-08-04 → open
            0 fill(s), 3 equity mark(s)
```

All 45 existing fills predate the boundary, so epoch 1 holds the entire traded
record to date and epoch 2 currently holds three equity marks and no fills.
**Epoch 2 has no track record yet, and the split is what makes that visible**
rather than letting epoch 1's 45 fills stand in for it.

## What was built

| File | Role |
|---|---|
| `store/schema.sql` → `paper_epochs` | the boundary table |
| `store/paperepoch.go` | `UpsertPaperEpoch`, `PaperEpochs`, `CurrentPaperEpoch`, `EpochBounds`, `InEpoch` |
| `pipeline/paperepoch.go` | the schedule, declared **in code** so a rebuilt database reconstructs identical boundaries; `ensureEpochs` runs every pass |
| `pipeline/paperrisk.go` | `tradedEdge` epoch-scoped |
| `api/paperepoch.go` | per-epoch segments on `GET /api/paper` |
| `cmd/sdmaint/paperepochs.go` | `sdmaint paper-epochs [-apply]` |

## Hard enforcement: what is actually scoped

**The sizing edge — scoped.** `tradedEdge` measures round trips inside the
current epoch only. This is the one that costs money if left alone: a quarter
Kelly fraction staked on a *previous* rule set's win rate does not merely
misreport the past, it sizes today's position on a number this strategy never
earned. Epoch 2 therefore reports **no measured edge** until it produces round
trips of its own, and sizing falls back to the equal-slice budget — which is the
correct answer for a strategy with no record.

**The drawdown breaker — deliberately NOT scoped, and this deviates from the
option as worded.** The chosen option said to scope both. Implementing it that
way turned out to conflict with the *other* choice: with the capital carried
across, re-basing the high-water mark would let the book lose `MaxDrawdown` in
epoch 1 and `MaxDrawdown` again in epoch 2 without the breaker firing once.

The two quantities answer different questions. The edge is a property of the
**strategy** — "what payoff shape do these rules produce?" — so it must be
re-measured when the rules change. Drawdown is a property of the **capital** —
"how far is this book below its high-water mark?" — and the capital is
explicitly continuous. A strategy change is not a reason to forgive the losses
that preceded it.

The breaker therefore reads the continuous curve; the epoch-scoped drawdown is
still computed and reported per segment, for attribution. If the reset is wanted
anyway, it is one line in `paperrisk.go` and the reasoning is recorded there.

**Straddling round trips — counted by neither epoch.** A trip bought under the
old rules and sold under the new is evidence about neither strategy. Filtering
to the epoch window leaves its sell unmatched and `MatchRoundTrips` drops it.
Reported as `straddlingRoundTrips` per segment so the omission is visible.

This matters immediately: the 21 carried positions were entered under epoch 1
and will be exited by epoch 2's barriers, so **the first post-boundary pass
produces up to 21 straddling exits**. They will inflate epoch 2's fill count
while contributing no round trips to its edge, which is the honest treatment and
is now labelled as such.

## A lookahead hole the tests caught

The first implementation scoped with a **lower bound only** (`ts >= from`). That
is correct while `at` is the present — the newest epoch has nothing after it —
and silently wrong for any measurement of a *past* epoch, where every later
strategy's trades leak in. A scoping filter that leaks the future is a lookahead
bug in the costume of a safeguard.

`TestStraddlingRoundTripIsCountedByNeitherEpoch` failed on exactly this. The
window is now closed at **both** ends (`store.InEpoch`), in `epochWindow`, so
correctness does not depend on the caller's choice of `at`.

## Verification

```bash
cd daemon && go build ./... && go vet ./... && go test ./... -count=1
```

Full suite: **exit code 0.**

```bash
cd daemon && go test ./internal/pipeline/ -run "Epoch|Straddling" -count=1 -v
```

```
--- PASS: TestEpochScopedEdgeIgnoresThePreviousStrategy
--- PASS: TestStraddlingRoundTripIsCountedByNeitherEpoch
--- PASS: TestEnsureEpochsIsIdempotent
--- PASS: TestNoEpochsMeansUnscoped
```

`TestEpochScopedEdgeIgnoresThePreviousStrategy` is the load-bearing one: 30
profitable round trips before the boundary yield a valid, well-sampled edge when
measured inside epoch 1, and **nothing at all** when measured inside epoch 2 —
same database, same trades.

`TestNoEpochsMeansUnscoped` guards the opposite failure: a database with no
declared boundaries must behave as one continuous record, never as a boundary at
timestamp zero. A filter that silently stops filtering is worse than no filter.

`TestEnsureEpochsIsIdempotent` pins the boundary constant to 1785801600 —
history does not move.

## Operational note

**The running daemon (PID 48936 at the time of writing) is on the pre-barrier
binary.** The boundary rows are applied and the code is committed to the tree,
but the trader will keep running epoch-1 rules until it is restarted. On the
first pass of the new binary the 21 carried positions meet the barriers and are
expected to be flushed as straddling exits.

Restarting the trader is not done here — it is a live-behaviour change and the
operator's call.

## What this did NOT do

- **No trades were invented, deleted or rewritten.** The split is additive: four
  rows in a new table.
- **No epoch-2 track record exists yet.** Zero fills. Nothing may be quoted about
  the barrier strategy's performance until it has one.
- **Book-wide `summary` and `money` still span both epochs** on `/api/paper`, on
  purpose — they describe the capital. `epochs[]` is where per-strategy numbers
  live, and `epochCaption` states which is which.
- **`briefing` and `fleethealth` still read the record book-wide.** They are
  internal monitoring, not published statistics; scoping them was out of scope
  and is recorded here as open.
