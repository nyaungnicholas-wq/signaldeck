# SignalDeck Audit Charter

Written before any agent is spawned, so agents do not optimize the wrong thing.
Read this first; quote the ground-truth facts from `ops/ground-truth/` verbatim
into every agent brief.

## Mission

Determine whether SignalDeck's prediction and trading pipeline is correct, safe, and worth repairing, without mutating production state.

## Non-goals

- No live trading changes
- No database writes
- No service restarts unless explicitly authorized
- No git state changes during investigation
- No model retraining unless separately scoped

## Success criteria

- Know whether the model has predictive edge after costs
- Know which findings are real, false, already fixed in source, or deploy-only
- Know which patches are safe to apply
- Know exact rollback steps

## Stop conditions

- Evidence of live DB writes
- Unexpected dirty git state in locked paths
- Failing safety-critical tests
- Contradictory evidence in crux metrics
- Missing rollback path

## Action classes

| Class   | Means                          | Requires                           |
|---------|----------------------------------|------------------------------------|
| READ_ONLY | Read-only queries, logs, source | None                               |
| CONFIG    | Flags, env vars                  | Approval                           |
| CODE      | Source changes                   | Tests plus patch review            |
| DATA      | Backfill, repair, annotation     | Backup plus dry run                |
| DEPLOY    | Rebuild, restart, replace binary | Rollback plan                      |
| MONEY     | Orders, fills, sizing, risk limits, paper-to-live | Human-owned only                   |

## Core rule

Agents investigate, humans decide, the owner computes the crux personally.