# Repair ledger
Opened 2026-08-26. Each item: symptom -> root cause -> fix -> verification.
Status is one of OPEN / IN PROGRESS / FIXED / VERIFIED / WONTFIX.

| id | symptom | status |
|---|---|---|
| R1 | S3 offsite destination committed but inert; 15 days no off-machine copy | **BLOCKED** - code is correct and complete; `SIGNALDECK_OFFSITE_S3` unset and `aws` has no credentials. Needs an input from Nicholas (see B2). |
| R2 | Only 5 symbols survive prediction gating | **NOT-A-DEFECT** - correct abstention, see E9. Must NOT be widened. |
| R3 | ai-analyst + sentiment-tagger erroring HTTP 410 | **FIXED** e5393b9 + e25505e - all 3 default models retired by the vendor; retiered to probed-live ids and collapsed the duplicate definition. Verification pending a scheduled run. |
| R4 | forecast-monitor denominator oscillates 291/997/2567 with the sweep | **OPEN - NOT TOUCHED, deliberately.** See E13: my first reading (wrong denominator) is unproven, the monitor may be correctly reporting the E9 abstention, and another session is editing this file (61f8bfb). Recorded, not guessed at. |
| R5 | OneDrive folder re-accumulating same-volume copies deleted 2026-08-23 | OPEN - 6.4GB regrown in 3 days. Bounded by compress_and_prune, so waste not corruption. Deferred behind R1. |
| R6 | One delisted symbol 400s the whole Alpaca batch; universe-poller failed daily | **VERIFIED** 8f7c56b - drop-and-retry at the choke point, 4 tests, mutation-checked |
| R7 | 31 LLM errors/day allegedly unescalated | **NOT-A-DEFECT - my error.** health.json named `failingWorkers:[sentiment-tagger]` and the offsite staleness, and a Discord transport is configured. See E15. |
