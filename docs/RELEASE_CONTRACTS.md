# Release contracts

`tools/docs_gate.py check` answers two different questions. Which one it answers
is chosen by `--contract`, and conflating them is what this file exists to stop.

| Contract | Question | Default |
| --- | --- | --- |
| `strict` | May this repository publish live accuracy **numbers**? | yes |
| `release` | Does this build **state the evidence it actually has**, correctly? | no |

Both run the identical check set. They differ in exactly one check,
`grader-status`, and in nothing else.

## Why there are two

The product is a research instrument whose design is to show refusals openly.
`strict` requires the grader to read `OK`. The refusal this repository has
carried since `2026-09-13T14:43` is honest, measured and exactly the thing the
site is built to display — and under `strict` it blocks the gate for as long as
it stands.

That is the incentive pointing the wrong way. A gate that can only go green by
making an honest refusal disappear is a gate that eventually gets switched off,
or worse, satisfied.

So `release` asks the question a release actually needs answered: not "are the
models working", but "is this build telling the truth about what was measured".

## What `release` is NOT

It is **not** `strict` with `REFUSED` added to a success list. A refusal is an
acceptable release state only when it is a **current, measured, attributed**
scientific result. Three impostors are rejected explicitly:

| Rejected | Why |
| --- | --- |
| `REFUSED_STALE` | Nobody measured recently. A stale checker must not impersonate a fresh scientific refusal. |
| `REFUSED` whose reason begins `CHECK UNAVAILABLE` | A **gate could not run**. The evidence state is UNKNOWN, not refused. Rendering unknown as a finding invents a verdict about a model. |
| `REFUSED` with no `refusal_reason` | Unattributable. A reader cannot check it. |
| `EMPTY`, blank, or any unrecognised status | Zero graded rows is not a publishable state, and an unknown status is never a pass — that is how "unknown" becomes "fine". |

The `CHECK UNAVAILABLE` prefix is not a convention this gate invented for itself.
It is written by `ops/accuracy-registry.sh` and `ops/grade.sh` whenever one of
their gates could not produce a verdict — a missing `collapsecheck` binary, a
renamed flag, a crashed `selection_honesty`, a registry that would not move into
place. Those two scripts and this gate agree on the spelling so that an outage
cannot arrive here wearing the costume of a measurement.

## What neither contract relaxes

- `no-hardcoded-live-accuracy` — a withheld figure typed into a document
- `forbidden-claims`
- `docs-index`
- `single-source-of-truth` — generated partials must match the snapshot
- `data-integrity`, `integrity-snapshot` — including the staleness bound

`grader-status`, `data-integrity` and `integrity-snapshot` remain in
`UNSUPPRESSIBLE_CHECKS` under both contracts: the allowlist can say "this
sentence is industry context", never "the grader is refusing, but publish
anyway".

So a `release` build still cannot carry a number the science does not support.
It can only carry an honest statement that the number is withheld.

## The scientific state is reported separately, always

Both contracts print, on success and on failure:

```
docs-gate: scientific evidence state = REFUSED (measured; graded_at 2026-09-13T14:42:10)
```

A green release build must not read as "the models work". Software readiness and
evidence state are different questions with different answers, and a reader is
entitled to both. `--json` carries the same two fields, `contract` and
`evidence_state`, for CI to surface.

## Which to run where

- **CI / pre-publication of figures:** `strict`. Unchanged, and every historical
  report of this gate was produced under it.
- **Release candidate / demo build:** `release`, plus `strict` reported
  alongside it as information rather than as a blocker.

```bash
python tools/docs_gate.py check                      # strict
python tools/docs_gate.py check --contract release   # release
```

## Contract version

`1.0.0`, introduced 2026-09-13. Tests: `tools/test_docs_gate.py::ReleaseContractTest`
(9 assertions, including one per impostor above). Changing what either contract
accepts requires a documented rationale here and an adversarial test, not an
edited assertion.
