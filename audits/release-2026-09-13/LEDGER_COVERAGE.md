# Ledger coverage — classification, not repair

Measured **2026-09-14T06:03Z** against the live database, read-only.

> Nothing in this document was fixed by writing to the ledger. The gap is
> classified where it stands. An entry appended today claiming to attest a July
> forecast would be exactly the forgery this chain exists to make detectable.

## Three different questions, three different states

They get conflated constantly, so they are named separately here.

| Question | Where it is answered | State today |
| --- | --- | --- |
| **Ledger completeness** — does every gradable forecast have a chain entry? | this document | **31 missing of 501,495** |
| **Chain integrity** — do the stored rows recompute? | `/api/ledger/verify` | intact, 0 failing anchors |
| **Forecast validity** — was the forecast any good? | `/api/accuracy` | **REFUSED** (collapsed window) |

An attested forecast can still be wrong. An intact chain can still be
incomplete. A complete ledger says nothing about skill.

## The measurement, with its cohort

A bare count of missing rows is not a measurement. The cohort matters more than
the number:

```
cohort        MODEL horizons only (1d, 1w)
              n_used > 0
              ts >= 1783155600  (2026-07-04, the first ledgered bar_ts)
denominator   501,495 eligible rows
UNATTESTED    31          (0.0062%)
  of those, already RESOLVED: 29   <- gradable as precommitted, nothing on the chain behind them
span          2026-07-06 -> 2026-09-11
```

### The trap in this query, recorded because I fell into it

`prediction_outcomes` also holds the **benchmark** rows under separate horizons
`1d#pm` and `1w#pm` (166,717 and 88,307 rows). Those are a mechanical baseline,
never attested, and **correctly** absent from the chain.

Joining `prediction_outcomes` to `prediction_ledger` without excluding them
reports **255,055 "unattested" rows**. That number is nonsense and it is the
first thing this query produces if you are not careful. It is ~8,200× the real
figure and would read as a catastrophe.

The `LEDGER_START` bound matters for the same reason: nothing before
2026-07-04 was ever ledgered, so counting from the beginning of the table
measures a policy, not a defect.

## What was actually at stake

29 resolved forecasts could be graded as precommitted with no attestation.
Against 501,495 that is **0.0058%** — statistically irrelevant to any verdict,
and entirely relevant to whether the precommitment claim is true as stated.

That is the whole point. The claim is not "almost all of our forecasts were
committed in advance".

## Why they stay missing

`ops/check-grader-health.ps1` has been reporting this as a bounded alarm
(`max 50`) for some time. An alarm is not a gate — nothing stopped those rows
being graded.

The repair (`03d3b4a`) makes the gap **unable to recur**: prediction, chain entry
and eligibility are now one transaction, so a failed append leaves nothing to
grade. It does not and must not touch the 31 that already exist:

- **backdating** them would fabricate evidence in the one place the project
  claims cannot be fabricated;
- **deleting** them would break the chain's linkage and destroy the record of
  what was actually served;
- **re-grading** to exclude them would change published historical verdicts,
  which is not an overnight decision and not one an agent should make.

They remain, counted, with their cohort stated.

## Reproducing this

```bash
.venv/Scripts/python.exe - <<'PY'
import sqlite3
LEDGER_START = 1783155600
con = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
q = """select count(*) from prediction_outcomes o
       left join prediction_ledger l
         on l.symbol_id=o.symbol_id and l.horizon=o.horizon and l.bar_ts=o.ts
       where l.seq is null and o.horizon not like '%#pm' and o.ts >= ?"""
print(con.execute(q, (LEDGER_START,)).fetchone()[0], "unattested")
PY
```

The count will drift as the system runs. Re-measure rather than quoting this
number; it is dated, not permanent.
