# Repair ledger
Opened 2026-08-26. Each item: symptom -> root cause -> fix -> verification.
Status is one of OPEN / IN PROGRESS / FIXED / VERIFIED / WONTFIX.

| id | symptom | status |
|---|---|---|
| R1 | S3 offsite destination committed but inert; 15 days no off-machine copy | OPEN |
| R2 | Only 5 symbols survive prediction gating | **NOT-A-DEFECT** - correct abstention, see E9. Must NOT be widened. |
| R3 | ai-analyst (x7) and sentiment-tagger (x24) erroring; many orphaned worker runs | OPEN |
| R4 | forecast-monitor counts the swept-wide universe, trainer works the pruned one | OPEN |
| R5 | OneDrive folder re-accumulating same-volume copies deleted 2026-08-23 | OPEN |
