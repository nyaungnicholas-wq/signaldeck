# History rewrite of public-launch, 2026-09-10

Six controls/ablations JSON files of 64-93 MB (per-row bootstrap arrays) were filtered out of the 16 commits e29eb6e..6e951c4 with git filter-branch; the shrunk versions were re-added on top (17th commit). Tree at the new head is byte-identical to the old head. The old tip stays reachable LOCALLY as keep/pre-rewrite-2026-09-10 (branch and tag, never pushed) so the daemon rows stamped with the old SHAs (b84670c: 499 worker runs, 14 structural calls, 252 ledger rows) keep resolving for the grader revision gate; keep/orphan-613bd2e5-2026-08-03 restores reachability of a pre-existing orphan (12,761 ledger rows, 2026-08-03, before the 2026-08-04 revision epoch). Backup bundle: ../_backups/signaldeck-public-launch-pre-rewrite-2026-09-10.bundle.

| old sha | new sha | subject |
|---|---|---|
| 87e7f5703d | 87e7f5703d | forecast repairs 2026-09-09: rv call bar must be the last completed session and is frozen  |
| 1b76fac144 | 1b76fac144 | research: forecast-plan audits â€” rv coverage per call day, structural cohort trace, dire |
| 63449b1be4 | 63449b1be4 | research: exp/metrics.py (accuracy, Brier, log loss, ECE, AUC, within-day AUC, prequential |
| 9dd47de95e | 9dd47de95e | research: frozen experiment manifest forecastplan-exp-v1; exp/vol_lib.py (numpy HAR with t |
| d8cca713a8 | d8cca713a8 | research: exp/structlabels.py (whole-series resolver labels, parity 11,892/0 vs labels.py) |
| 6c157bbc26 | 6c157bbc26 | audits: 2026-09-10 forecast repair ledger (F1-F7: rv call bar, freeze-once, gate window, s |
| 15909590d9 | 15909590d9 | research: exp/data_structural.py (resolver labels, persistence nulls, incumbent calls port |
| e5aaf42db1 | e5aaf42db1 | research: exp/run_direction.py â€” nested walk-forward direction campaign (outer blocks, i |
| 9bdde16aaa | 9bdde16aaa | research: exp/direction_controls.py â€” within-day label shuffle, 21-session feature lag a |
| fc9effbb28 | fc9effbb28 | research: exp/run_structural.py (labels vs persistence vs incumbent, class and transition  |
| 98237e9a53 | 7ee48bb326 | research: campaign outputs under manifest forecastplan-exp-v1 â€” direction (h1/h5: 6 bloc |
| 5c9ba127ae | 3ebef8560b | research: exp/run_vol.py â€” walk-forward QLIKE comparison of HAR candidates (market term, |
| b84670c978 | c4b67abac4 | research: evidence-status report 2026-09-10 (evidence matrix, root causes, campaign result |
| 80b6b08396 | d5b9661c2e | audits+research: record the deploy of 87e7f57 (running b84670c, verified) and the first li |
| 9b78ce0c08 | 7e6fe197a7 | research: strip per-row skill arrays from the controls/ablations JSON (64-93 MB each -> 13 |
| 6e951c4a1c | 8fb4b8a75d | research: full-universe volatility candidate study (1,292 symbols, 1,382 day clusters): V1 |
| (none) | 44edb0b9a6 | re-add the shrunk controls/ablations JSON |
