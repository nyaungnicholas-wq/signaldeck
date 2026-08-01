# EIGHTY PERCENT — a research loop for a defensible 80% hit rate

Paste this whole file as a single prompt. It is self-contained: assume the agent
receiving it knows nothing about this project or the conversation that produced it.

---

You are running a long-horizon research loop on SignalDeck with ONE goal:

> **80% precision on issued directional calls, at a stated horizon, measured
> out-of-sample on a pre-registered rule, with a multiplicity-corrected interval
> — and an abstention rate you are free to choose.**

Read that twice. Every clause is load-bearing, and section 0 explains why the
obvious reading of "80% accurate" is a trap rather than a target.

## 0. Why the goal is worded that way

**80% raw directional accuracy on next-day moves is not a stretch goal, it is a
red flag.** Published out-of-sample consensus for daily direction is 54-58%. A
controlled comparison across 918 experiments found a MEAN directional accuracy of
50.08% — indistinguishable from a coin flip — and concluded that MSE-trained deep
architectures produce forecasts equivalent to chance on hourly data. Papers
reporting 71-73% do so on denoised index series, using preprocessing choices that
are themselves fitted to the sample.

This system has already measured itself, and the numbers are the honest ones:

| measured on this repo | value |
|---|---|
| directional-ensemble, 1d | 52.8% |
| naive "always-down" baseline | 55.0% |
| **edge vs naive** | **-5.5pp** |
| Brier skill | -13.3% |
| IC, day-clustered | -0.018, CI spans zero |
| design effect (measured) | 6.99 — 11,388 rows are 1,629 observations |

The model currently loses to guessing the majority class. So any process that
suddenly reports 80% has, with overwhelming probability, done one of four things:

1. **Exploited class imbalance.** Accuracy weights per-class hit rates by class
   size, so a model that always votes the majority class scores the base rate
   having learned nothing. The literature calls this *unskilled classification*.
   If 80% of labelled days are "up", a constant "up" model is 80% accurate and
   worth exactly zero.
2. **Picked a persistent target.** "Will this stock still be on the same side of
   its 200-day MA in 21 sessions?" is ~73% correct by predicting no change,
   because SMA200 state is sticky. High accuracy, zero skill. This repo registers
   precisely such kinds and grades them against a persistence null for that reason.
3. **Leaked the future.** A feature scaled on the full sample, a label built from
   a bar the model could see, a universe selected with hindsight (survivorship —
   this repo's own track record is flagged `survivorship: exposed`).
4. **Overfitted the search.** The probability of selecting an overfit strategy
   rises rapidly with the number of trials. Report the best of 200 configurations
   without paying for the 200, and the edge is a selection artifact.

**Accuracy alone is therefore never evidence in this loop.** Every claim carries
its base rate, its abstention rate, and a corrected interval, or it is not a claim.

## 1. The honest route to 80%

Precision at low recall. Do not try to be right about every day — issue few calls,
abstain constantly, and be right when you fire.

Let `p` be precision on issued calls, `r` the fraction of opportunities where a
call is issued, and `b` the base rate of the predicted class *within the issued
subset*. The target is:

- `p >= 0.80`
- `p - b >= 0.10`
- lower bound of the corrected interval strictly above `b`

**`r` may be tiny. `r = 0.02` is a fine answer.** A rule firing on 2% of
symbol-days and right 80% of the time is a genuine edge. A rule firing always and
right 80% of the time against an 80% base rate is nothing at all.

This is not a workaround for a hard goal — it is how the goal is legitimately met,
and the machinery already exists here: conviction bands, where the registered
trend21 claim rises from 73.1% at any conviction to 97.2% at >= 0.9. Your job is
to establish whether bands like that survive honest measurement. Note the warning
already recorded against them: the most ACCURATE trend21 band carries a NEGATIVE
mean forward return, because high conviction means price is far from its average
and extended names mean-revert. **Accuracy and profit are different quantities.**

## 2. Non-negotiable method

**Pre-register before you look.** Write the rule, horizon, universe, entry and
abstention conditions, and the claimed band to the chain BEFORE grading. A claim
registered after the outcome is known is not a prediction, it is a description.
PREREGISTRATION.md makes the chain authoritative over prose; nothing in the repo
overrides it.

**Purge and embargo.** Use combinatorially purged cross-validation. Any training
sample whose label window overlaps the test window is dropped, and an embargo gap
follows the test set. Plain k-fold leaks on overlapping financial labels, and CPCV
is the method shown to best mitigate that leakage. Fit every scaler and selector
inside the training fold only.

**Count observations, not rows.** Four hundred forecasts resolving on one day are
ONE market observation. Cluster by (symbol, UTC day) and derive an effective N
from the measured design effect — here, 6.99, which turns 11,388 rows into 1,629
observations. Raw N is not a sample size, and treating it as one is the fastest
way to a confident wrong answer.

**Pay for every trial.** Track the number of configurations evaluated and report
the Deflated Sharpe Ratio and the Probability of Backtest Overfitting alongside
any result. DSR corrects for selection bias, trial count, sample length, skew and
kurtosis. A strategy that fails its DSR threshold was not discovered, it was
selected.

**Beat the right null.** Not 50%. Beat the majority-class base rate of the subset
where a call was actually issued, and beat the persistence baseline already frozen
in this repo's chain. State both numbers next to the result, always.

**Cost it.** Report edge after spread, commission, slippage and turnover, at the
size you intend to trade. An 80% hit rate on 5bp moves against a 10bp spread is a
losing strategy that looks like a winning one.

## 3. The loop

Repeat indefinitely. Each cycle is one hypothesis, honestly killed or honestly
kept. You should expect to kill nearly all of them — that is the loop working, not
failing.

**Step 1 — one hypothesis, from a mechanism.** State in a sentence why it should
work: a liquidity constraint, a flow imbalance, a participant behaviour, a
slow-diffusing information source. A pattern with no mechanism is a coincidence
you have not caught yet. Record it before testing it.

**Step 2 — define abstention FIRST.** State when the rule declines to call, before
measuring anything. Abstention is what buys precision; defining it after seeing
results is fitting.

**Step 3 — build features under strict as-of discipline.** Every value must be
computable at the decision timestamp. Verify by reconstructing one sample by hand
and confirming no input postdates the call.

**Step 4 — purged CPCV, then a sealed era.** Hold out the most recent 20% and do
not touch it. You may look ONCE. Looking twice makes it training data.

**Step 5 — grade through the registry.** Use `tools/accuracy_registry.py`. It
refuses to publish unless the grader matches the digest pinned in the chain, and
it prices multiplicity for you. Do not bypass it; the bypass is the bug it exists
to prevent.

**Step 6 — record the result whether or not it worked.** A killed hypothesis is
evidence and must be persisted with its numbers. A search whose judgments cannot
be counted from the database is an unverifiable null result — this repo refuses to
publish beside one, and has done so for real.

**Step 7 — only then consider deployment.** Paper-trade with costs, at size, for
at least one full horizon before repeating any claim of edge anywhere user-facing.

## 4. Acceptance criteria

All nine must hold simultaneously, on the sealed era, reproducible from committed
code plus committed data:

- [ ] Precision on issued calls >= 0.80
- [ ] Precision minus issued-subset base rate >= 0.10
- [ ] Lower bound of the day-clustered, multiplicity-corrected interval > base rate
- [ ] Independent observations >= 30 AND distinct days >= 10 (this repo's floors)
- [ ] Deflated Sharpe Ratio clears its threshold for the trial count actually run
- [ ] Probability of Backtest Overfitting < 0.5
- [ ] Edge survives spread + commission + slippage at intended size
- [ ] The rule was pre-registered on the chain before grading
- [ ] The result reproduces from a cold clone

Fewer than nine is not 80%. It is a hypothesis.

## 5. Forbidden

- Reporting accuracy without its base rate and abstention rate.
- Tuning any threshold on the sealed era.
- Dropping symbols, days or outliers after seeing their effect.
- Backfilling a label, baseline or judgment once the outcome is known.
- Weakening, deleting or special-casing any check, threshold or refusal in this
  repository to make a number look better. This is the worst action available to
  you; it converts a measurement system into a marketing system.
- Publishing a verdict from rows whose writing binary is `+dirty` or unstamped.
- Quietly redefining the target so a previously unreachable number becomes reachable.

## 6. What success actually looks like

You will likely spend a long time finding nothing. The correct output of a
disciplined 48-rule grid search is usually "nothing survived Bonferroni
correction, regime-survival and the counterfactual" — and this repo already
records exactly that, correctly, as a result rather than a failure.

If you reach 80% precision on 2% of opportunities with all nine boxes ticked, you
have something rare and real. If you reach 80% on every opportunity, you have made
a mistake you have not found yet — return to section 0 and identify which of the
four traps you are in.

An airtight negative result is worth more than a positive one that is not, because
only one of the two can be traded.

---

## Sources

- Controlled comparison of deep architectures, 918 experiments (mean directional
  accuracy 50.08%): https://arxiv.org/pdf/2603.16886
- Evaluation of deep learning models for stock market trend prediction:
  https://arxiv.org/html/2408.12408v1
- Predicting daily stock price directions with deep learning models:
  https://www.sciencedirect.com/science/article/pii/S2666827025001276
- Bailey & Lopez de Prado, *The Deflated Sharpe Ratio*:
  https://papers.ssrn.com/sol3/papers.cfm?abstract_id=2460551
- Bailey, *How backtest overfitting in finance leads to false discoveries*:
  https://rss.onlinelibrary.wiley.com/doi/abs/10.1111/1740-9713.01588
- Backtest overfitting in the ML era — out-of-sample testing methods, CPCV, PBO:
  https://www.sciencedirect.com/science/article/abs/pii/S0950705124011110
- Failure of classification accuracy for imbalanced class distributions:
  https://machinelearningmastery.com/failure-of-accuracy-for-imbalanced-class-distributions/
- Class imbalance and "unskilled classification":
  https://www.sciencedirect.com/science/article/pii/S1053811923004044
- Asymmetric loss for directional forecasting (why MSE/MAE fail to incentivise
  directional correctness): https://www.mdpi.com/2673-2688/6/10/268
