<!-- Every PR passes the review levels defined in ops/REVIEW_LIFECYCLE.md.
     A level is not "done" until its checklist has written evidence (test output,
     reproduction, or a measured number) — assertions without evidence are
     UNVERIFIED and block merge. -->

## Which review levels apply, and why? (required)

<!-- State which of the three levels this PR must clear and the reasoning.
     Level 1 always applies. Level 2 applies to any change touching signals,
     features, models, or grading. Level 3 applies to anything that deploys.
     Example: "Levels 1+2 — adds a feature column to the gbm training set;
     no deploy in this PR." -->

Levels:
Why:

## Level 1 — Code Review ([ops/REVIEW_LIFECYCLE.md](../ops/REVIEW_LIFECYCLE.md#level-1--code-review))

- [ ] Correctness, architecture fit, minimal diff
- [ ] `go build ./...` and `go test ./...` green in `daemon/` (paste output or run link)
- [ ] `npm run lint` + affected Playwright specs green in `web/` (or N/A — no web change)
- [ ] Security pass done for any change touching auth, env parsing, exports, or new endpoints (the A6/A9/A10 class)
- [ ] No new unauthenticated endpoint — or the written justification is in this PR description

## Level 2 — Quant Review ([ops/REVIEW_LIFECYCLE.md](../ops/REVIEW_LIFECYCLE.md#level-2--quant-review-any-change-touching-signals-features-models-grading))

<!-- Required for any change touching signals, features, models, or grading. Check N/A otherwise. -->

- [ ] N/A — this PR does not touch signals, features, models, or grading
- [ ] Leakage check: purged splits (`forecast.Evaluate` standard); no model-output or label-derived keys in training (extended `gbm.SelfReferentialKey`, not worked around)
- [ ] Statistics: published intervals / promotion gates use cluster-robust n via `clusterstat` — no raw-row Wilson intervals (A1/A2/A3 class)
- [ ] Gate/serve identity: the signal validated by a gate is byte-identical to the signal served (A5 class)
- [ ] New features: proven non-constant over the live features table (mode=ro), and the economic hypothesis is stated below (A8 class)

Economic hypothesis (new features only):

## Level 3 — Production Review ([ops/REVIEW_LIFECYCLE.md](../ops/REVIEW_LIFECYCLE.md#level-3--production-review-before-deploy))

<!-- Required before deploy. Check N/A if this PR does not deploy. -->

- [ ] N/A — this PR does not deploy
- [ ] Failure simulation run: data outage (4xx/5xx), stale cache, missing candles, clock drift, daemon restart mid-cycle — degrades safely (fail-closed for gates, fail-quiet for display)
- [ ] Endpoint budget: any new endpoint has auth or rate limiting, a response deadline, and is in `ops/pre-publish-scan.sh` scope if it can leak data
- [ ] Concept drift: if a served model changed, the canary runs its full distinct-day floor before promotion — no manual promotion
- [ ] Relevant slice of the audit checklist re-run and the result appended to `audits/` with the date

## Evidence

<!-- Paste the test output, reproduction, or measured numbers backing the
     checked boxes above. Unbacked checkmarks are UNVERIFIED and block merge
     (ops/REVIEW_LIFECYCLE.md). -->
