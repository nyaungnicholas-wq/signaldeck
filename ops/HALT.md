# The kill switch

`ops/HALT` is the halt file. **If a file exists at this path, SignalDeck opens
no new positions.**

Implementation: `daemon/internal/killswitch`. Policy: `RISK_POLICY.md` §4.

## Trip it

```bash
echo "why you are halting" > ops/HALT
```

Takes effect on the **next order** — no restart, no signal, no daemon
interaction. The text you write becomes the audited reason and is snapshotted
into the `ev_decisions` ledger with every refusal it causes.

## Clear it

```bash
rm ops/HALT
```

Trading resumes on the next pass.

## Check it

```bash
ls -l ops/HALT 2>/dev/null && cat ops/HALT || echo "not halted"
```

Refusals it caused:

```sql
SELECT ts, symbol, reason FROM ev_decisions
WHERE reason = 'kill-switch-halted' ORDER BY seq DESC LIMIT 20;
```

## Without a file

`SIGNALDECK_HALT=1` halts too, for containers and one-off runs with no writable
path. It can only ever **halt** — `SIGNALDECK_HALT=0` will not clear a halt file
sitting on disk.

`SIGNALDECK_KILL_SWITCH=/some/other/path` moves the file.

## What it does and does not do

- **Refuses entries.** Every new position is blocked while halted.
- **Does NOT block exits.** A halt that trapped the book inside the position it
  was tripped by would be a bigger risk than the one it controls.
- **Does NOT flatten.** It stops new risk; it does not liquidate. The −12%
  "flatten and halt" rung in `RISK_POLICY.md` §3 is SPEC ONLY for this reason.
- **Fails closed.** If the switch cannot be read at all — permissions, a missing
  volume, a malformed path — the system **halts**. An unreadable check is not a
  passing check.

## Scope

This halts the **paper trader**, which is the only executor in this repository.
There is no broker order path here. `ops/HALT` is gitignored: a halt is local
runtime state and must not travel in a commit.
