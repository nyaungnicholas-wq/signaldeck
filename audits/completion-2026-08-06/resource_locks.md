# Resource Locks & Conflict Log — 2026-08-06

## Live concurrency hazard (this is not hypothetical)

Two autonomous Windows Scheduled Tasks write into this working tree while the completion run is
executing. Both were observed changing files mid-session:

| Writer | Writes | Observed |
|---|---|---|
| `SignalDeck Eighty Loop` (hourly, RUNNING) | `research/eighty/h02*.py` | `h0266–h0269.py` appeared between 13:00 and 13:55 |
| `SignalDeck Accuracy` (daily 14:05) | `README.md`, `data/accuracy_registry.json` | README rewritten at 14:05:06, verdict changed `NO SKILL` → `FAILED` |

**Consequence:** `README.md` and `research/eighty/**` were declared OFF-LIMITS to every agent and to
the orchestrator. No repair touches them. Nothing was `git add`ed or committed, so neither writer
can have its work captured or clobbered by this session.

## Lock assignment

| Resource | Holder | Mode |
|---|---|---|
| `daemon/internal/marketcal/marketcal.go` | orchestrator | write (R2) |
| `daemon/internal/workers/schedule.go` | orchestrator | write (R2) |
| `daemon/internal/workers/schedule_check_test.go` | orchestrator | write (R2) |
| `daemon/cmd/signaldeckd/run.go` | orchestrator | write (R3) |
| `daemon/cmd/signaldeckd/staleness_test.go` | orchestrator | write (R3) |
| `daemon/internal/pipeline/paper.go` | orchestrator | write (R5 — risk path, deliberately not delegated) |
| `daemon/internal/pipeline/paper_test.go` | orchestrator | write (R5) |
| `daemon/internal/mcp/attack_test.go` | orchestrator | write (NEW-1) |
| `tools/docs_gate.py` | agent `repair:docs-gate` | write (SD-012) |
| `daemon/internal/srchealth/**` | agent `repair:source-health` | write (SD-H35) |
| `README.md`, `research/eighty/**` | **external scheduled tasks** | FORBIDDEN to all |
| `data/signaldeck.db` | live daemon (pid 22220) | read-only (`?mode=ro`) for all |
| everything else | — | read-only |

Lock sets are disjoint by construction. Every other agent in the run was read-only.

## Conflict log

**No write-write conflicts occurred.** The two repair lanes touched disjoint files, and the
orchestrator's files were declared locked in every agent brief.

One *sequencing* note worth recording: the R2 repair (scheduler) and the `repair:source-health`
lane (SD-H35) address the same failure from opposite ends — R2 makes the FINRA workers schedulable
again, SD-H35 makes their staleness visible if they stop. They were deliberately kept in separate
files so they could run in parallel; neither depends on the other's edit.

## Rollback

Every repair is source-only and reversible:

```bash
git checkout -- daemon/internal/marketcal/marketcal.go \
                daemon/internal/workers/schedule.go \
                daemon/internal/workers/schedule_check_test.go \
                daemon/cmd/signaldeckd/run.go \
                daemon/cmd/signaldeckd/staleness_test.go \
                daemon/internal/pipeline/paper.go \
                daemon/internal/pipeline/paper_test.go \
                daemon/internal/mcp/attack_test.go \
                daemon/internal/srchealth/ tools/docs_gate.py
```

No binary was rebuilt, no service restarted, no database written, no git state changed. The running
daemon is unaffected by every change in this run — which is also why none of these fixes is live.
