# SignalDeck Score-Loop Superprompt
<!-- SUPERSEDED-SNAPSHOT -->
> ## 📛 SUPERSEDED LIVE RECORD — HISTORICAL
> **Marked 2026-08-04 by P2.** Any live accuracy, baseline or sample size quoted
> below is the record **as it stood when this document was written**, not the
> current one. It is kept because a dated record is evidence; it is labelled
> because four such records were once in circulation with nothing to tell them
> apart (FC1).
>
> **The one authoritative live record is `partials/live_accuracy.md`**, generated
> from `data/accuracy_registry.json` by `tools/live_accuracy.py`. **If you are following this document as a brief, take every accuracy figure from that partial and include it rather than restating it — do not copy the numbers below into anything you produce.** Reconciliation:
> `proofs/P2_LIVE_RECORD_RECONCILIATION.md`.


Drive every SignalDeck rubric board to **≥80/100 on method rigor**, looping until each board is either at target or provably blocked.

**Workflow script:** `tools/score_loop_workflow.js` — run via the `Workflow` tool with
`{scriptPath: "/Users/natalienyaung/claude code/signaldeck/tools/score_loop_workflow.js"}`.

---

## THE ONE RULE THAT DEFINES THIS LOOP

**These scores grade the RIGOR OF THE METHOD. They never grade predictive accuracy or profitability.**

This is not a softening of the goal — it is the only version of the goal that is achievable and honest, and it follows directly from SignalDeck's own verified findings:

- 1-day direction is capped around **55%**, proven two independent ways (Sheppard's arcsin bound, and an exhaustive walk-forward test over ~1M observations).
- The live prequential record is **46.7% over 8,191 independent symbol-days, with negative Brier skill** — the directional ensemble has proven *negative* skill on live data.
- trend21's *most accurate* band (97.2%) carries a **negative** mean 21-day forward return. High accuracy and profitability are different claims, and only the first is true.

Therefore: **an honest, well-powered null result is top-tier evidence and must score HIGH.** A high accuracy number resting on an unmatched null, overlapping samples, or a survivorship-contaminated universe is worthless and must score LOW.

Chasing accuracy in this loop would reproduce exactly the failure already on record: *"report the real number, keep improving — NOT loop until it hits."*

---

## Non-negotiable rules

1. **No score theater.** A board moves only if the underlying code, data handling, or process actually became more rigorous. Rewriting a disclosure so a weakness reads better, without fixing the mechanism it discloses, is the single most likely way this loop fails. Every round runs an adversarial Verify agent whose only job is to catch cosmetic edits; anything it flags must not raise a score.
2. **Never tune to the number.** Do not adjust a threshold, re-band, or re-label so a metric clears a bar. Do not go hunting for a new predictor merely because the current ones show no edge. Candidate fixes whose only justification is "this raises the score" must be recorded in `rejected`, not applied.
3. **Re-score from the repo, never from the last audit.** Every Score phase re-derives numbers by reading current code and running `tools/accuracy_registry.py` fresh.
4. **Be the hostile reviewer, not the defense.** Score against the weakest remaining evidence in each category.
5. **Fixes must name files.** Reject any plan item that says "improve X" without exact paths and the specific change.
6. **Sequence overlapping code.** Statistical validity, Scientific credibility, and Research quality fixes touch the same prediction/backtest paths — they are applied sequentially, never in parallel.
7. **Leave commits to the human.** The workflow edits files; it never runs `git commit`. Review the diff before committing anything.

## Stop conditions — the loop does not stop just because it got hard

- A category that no code change can move is marked **BLOCKED** with a plain-language reason, and the loop stops spending rounds on it while continuing on everything movable.
- A round that produces zero legitimate fixes does **not** end the run — it records what it refused and re-researches next round with updated blocked/rejected memory.
- The run ends only when every non-blocked board is ≥80, or when *every* remaining category is BLOCKED.
- Never invent busywork to look productive. "This is structurally blocked because X" is a valid and valuable result.

---

## Baseline (from `audits/2026-07-26-reaudit.md` §3 — re-verify, don't trust blindly)

| Category | Score | Gap to 80 |
|---|---|---|
| Overall architecture | 57 | 23 |
| Research quality | 27 | 53 |
| Statistical validity | 19 | 61 |
| Software engineering | 57 | 23 |
| Production readiness | 21 | 59 |
| Institutional readiness | 15 | 65 |
| Competitive moat | 19 | 61 |
| Scalability | 28 | 52 |
| Explainability | 79 | 1 |
| Scientific credibility | 29 | 51 |

Statistical validity, Institutional readiness, and Competitive moat are the deepest holes. **Explainability is already at target — do not spend effort there.**

## Known open defect the Research phase must investigate

`research_loop_hypotheses` currently has **zero rows** despite `pipeline.ResearchLoop` being built with Bonferroni correction, era coverage, and a Bayes ledger over 163k weekly observations. Determine whether the worker is running, erroring, day-gated, or refusing by design (it refuses below 2,000 observations). **A research engine that has never emitted a single hypothesis is a first-class Research quality defect**, not a footnote.

---

## OmniRoute delegation (free local model pool)

The loop offloads its **pure-text judgment** to the OmniRoute gateway at `localhost:20128`, which is free and does not consume Claude quota.

**What goes to OmniRoute:** the score-theater screen in Plan, and the independent REAL-vs-COSMETIC second vote in Verify. Both operate on a payload (candidate-fix list, or a git diff) that a Claude agent already gathered.

**What never goes to OmniRoute:** Research, Fix, and Score. `omniroute chat` is a stateless completion call with **no file access and no tools**, and all three phases read or edit the repo. Delegating them is not a cost tradeoff — it is impossible.

Rules the agents follow:
- **Always `-m auto`.** Never pin a `provider/model` — pinned requests do not fall back, so one flaky upstream fails the call. `auto` self-adapts and runs ~800ms warm.
- **Always `--file`**, never shell-interpolate a diff or JSON into the command line.
- The reply is **one independent vote from a fast free helper**, never the final answer. The Claude agent owns the verdict.
- On **disagreement between the two votes, `real_change` is forced to false** — a change that isn't clearly real doesn't get to raise a score.
- If the call fails, proceed on Claude's own judgment and say so. Never block, never retry more than once.

Validated 2026-07-27: the free pool correctly classified both a disclosure-only diff (COSMETIC) and a non-overlapping-windows fix (REAL), ~1.2s each.

## Round shape (encoded in the script)

1. **Research** — three parallel agents, each owning a narrow cluster of categories with named files it must actually open, each returning a structured verdict including `movable` / `blocked_reason`. *(This replaced a single agent asked to summarize all ten categories in under 400 words, which is why earlier rounds produced unusable output.)*
2. **Plan** — max 6 file-named fixes, each carrying a `rigor_justification`; theater candidates recorded as `rejected`.
3. **Fix → Verify** — pipelined per fix, so each edit is adversarially checked for real mechanism change as soon as it lands.
4. **Score** — hostile re-review of all 10 boards from current repo state.

## How to run

- One invocation (up to 8 internal rounds): `Workflow({ scriptPath: "/Users/natalienyaung/claude code/signaldeck/tools/score_loop_workflow.js" })`
- Sustained: `/loop` this superprompt so it re-invokes across turns.
- After each invocation: review the uncommitted diff, decide what to keep, commit yourself.

## Report format expected back each round

- All 10 current scores with deltas vs previous round, plus accuracy_registry verdict summary.
- What actually changed (file list, one line each), separated from what was **rejected as cosmetic**.
- Any newly BLOCKED category with its structural reason.
- What still blocks the lowest 2–3 categories, in plain language — not hedged, not inflated.
