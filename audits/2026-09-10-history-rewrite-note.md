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

## Addendum, 2026-09-10 (later): keep refs deleted at Nicholas's instruction

- Deleted `refs/heads/keep/pre-rewrite-2026-09-10`, `refs/tags/keep/pre-rewrite-2026-09-10` and `refs/heads/keep/orphan-613bd2e5-2026-08-03`. The 16 pre-rewrite commits (old shas 6e951c4..87e7f570) and the 2026-08-03 orphan 613bd2e5 are now reachable from no ref. Their objects still resolve today and will until a `git gc` prunes them (the public-launch and HEAD reflogs hold them for 30 days by default, so the first gc after about 2026-10-10 drops them), which makes the effect below delayed and silent.
- Grader effect once pruned: the pinned grader (tools/accuracy_registry.py, REVISION_EPOCH 2026-08-04) strips a family when any post-epoch row cites a commit the repo cannot resolve. Rows stamped b84670c9 are post-epoch: prediction_ledger 252 rows, all 1w, predicted 2026-09-10; regime_outcomes 7 liquidity21-crypto and 7 trend21-crypto, day 2026-09-10, unresolved. So the 1w direction verdict and those two crypto kinds will be stripped permanently. The 12,761 prediction rows and 3 regime rows stamped 613bd2e5 are dated 2026-08-03, before the epoch: exempt.
- Remedies, neither taken: (a) restore attribution with `git fetch "<_backups>/signaldeck-public-launch-pre-rewrite-2026-09-10.bundle" refs/heads/public-launch:refs/heads/keep/pre-rewrite-2026-09-10` and `git fetch "<_backups>/signaldeck-orphan-613bd2e5-2026-08-03.bundle" refs/tags/tmp-orphan-613bd2e5:refs/heads/keep/orphan-613bd2e5-2026-08-03` (the 43 KB orphan bundle was written today for this purpose; `_backups` is `Desktop/claude code/_backups`); (b) chain a revision-epoch-correction, which is what the hook prescribes after an override, but `daemon/cmd/prereg-amend` hardcodes the 2026-07-27 to 2026-08-04 move, so a new move needs its own spec and note. Nicholas's decision.
- Gate defect found on the way: `ops/githooks/reference-transaction` let all three deletions through WITHOUT `SIGNALDECK_ALLOW_HISTORY_REWRITE`, and let a scratch loose tag be deleted too. Measured with a logging hook: git 2.55.0.windows.3 feeds `tag -d` and `branch -D` as `0000... 0000... <ref>` (no old value), and pass one read a zero old value as a creation. Fixed in this commit: a zero-to-zero line whose ref still resolves in the prepared state is treated as a deletion. Verified by feeding the hook a zero-to-zero line for an existing ref (refused, naming 613bd2e5 and b84670c) and for a missing ref (allowed). The override was used only to remove two scratch tags (at HEAD and at 613bd2e5) that the hook would now correctly refuse because of the pre-existing orphans; it was never used on the keep refs, which the hook failed to see.
