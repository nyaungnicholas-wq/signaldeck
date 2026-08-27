# Repair ledger
Opened 2026-08-26. Each item: symptom -> root cause -> fix -> verification.
Status is one of OPEN / IN PROGRESS / FIXED / VERIFIED / WONTFIX.

| id | symptom | status |
|---|---|---|
| R1 | S3 offsite destination committed but inert; 15 days no off-machine copy | **BLOCKED** - code is correct and complete; `SIGNALDECK_OFFSITE_S3` unset and `aws` has no credentials. Needs an input from Nicholas (see B2). |
| R2 | Only 5 symbols survive prediction gating | **NOT-A-DEFECT** - correct abstention, see E9. Must NOT be widened. |
| R3 | ai-analyst + sentiment-tagger erroring HTTP 410 | **VERIFIED** e5393b9 + e25505e - both workers now `ok` in production (tagged 60 headlines / wrote analyst brief); /api/ai/status shows 79 calls, lastError empty. See E14, E16. |
| R4 | forecast-monitor denominator | **NOT-A-DEFECT** - the monitor is correct and corroborates E9 from the outcomes side. See E17. |
| R5 | OneDrive folder re-accumulating same-volume copies | **WONTFIX (superseded by R1)** - bounded, deliberate and documented, and it stops entirely once an offsite destination is set. See E18. |
| R6 | One delisted symbol 400s the whole Alpaca batch; universe-poller failed daily | **VERIFIED** 8f7c56b - drop-and-retry at the choke point, 4 tests, mutation-checked |
| R7 | 31 LLM errors/day allegedly unescalated | **NOT-A-DEFECT - my error.** health.json named `failingWorkers:[sentiment-tagger]` and the offsite staleness, and a Discord transport is configured. See E15. |
| R8 | Regime calls on pruned symbols retried forever; 31-day silent stall, survivorship hole | **FIXED** 082e6dc - retire+count past a 3x grace window; 4 tests, mutation-checked BOTH ways; deployed. Production confirmation pending the in-flight pass. See E22, E23. |
