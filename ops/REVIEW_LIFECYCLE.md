# Review Lifecycle — every PR / major feature

The 2026-07-26 adversarial audit (`audits/2026-07-26-reaudit.md`) is not a one-off.
Every PR or major feature passes three review levels before merge. A level is not
"done" until its checklist has written evidence (test output, reproduction, or a
measured number) — assertions without evidence are marked UNVERIFIED and block merge.

## Level 1 — Code Review
- Correctness, architecture fit, minimal diff.
- `go build ./...` and `go test ./...` green in `daemon/`; `npm run lint` + affected
  Playwright specs in `web/`.
- Security pass on any change touching auth, env parsing, exports, or new endpoints
  (the A6/A9/A10 class of bugs all entered through these).
- No new unauthenticated endpoint without an explicit written justification.

## Level 2 — Quant Review (any change touching signals, features, models, grading)
- Leakage check: purged splits (the `forecast.Evaluate` standard = same purge as
  gbm/alphax/metalabel), no model-output or label-derived keys in training
  (extend `gbm.SelfReferentialKey`, don't work around it).
- Statistics: any published interval or promotion gate must use cluster-robust n
  via `clusterstat` — raw-row Wilson intervals are banned (A1/A2/A3 class).
- The signal validated by a gate must be byte-identical to the signal served
  (A5 class: gate-on-X, serve-on-Y is an automatic block).
- New features: prove non-constant over the live features table (mode=ro) and
  state the economic hypothesis in the PR description (A8 class).

## Level 3 — Production Review (before deploy)
- Failure simulation: data outage (provider 4xx/5xx), stale cache, missing candles,
  clock drift, daemon restart mid-cycle. The change must degrade safely (fail-closed
  for gates, fail-quiet for display).
- Endpoint budget: any new endpoint has auth or rate limiting, a response deadline,
  and appears in `ops/pre-publish-scan.sh` scope if it can leak data.
- Concept drift: if the change alters a served model, the canary must run its full
  distinct-day floor before promotion — no manual promotion.
- Re-run the relevant slice of the audit checklist and append the result to
  `audits/` with the date.

## Cadence
- Per-PR: Levels 1–3 as applicable above.
- Quarterly (or after any incident): full adversarial re-audit in the style of
  `audits/2026-07-26-reaudit.md`, appended to `audits/`, findings tracked to
  fixed/refuted — never left "proposed" past the next audit. Enforced by
  `tools/audit_register.py`, which CI runs in the `docs-gate` job: a red run
  is the finding, and the vocabulary is never widened to clear it.
