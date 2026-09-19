# forecastplan-exp-v1 — clarifications after independent review (2026-09-10)

An independent worker review (reason lane, 2026-09-10 02:1x UTC) of `manifest.json` raised nine
points. None changes a rule, a threshold, a dataset or a metric, so the manifest id stays
`forecastplan-exp-v1`; this addendum records how each point is read by the implementation.
Any future change that alters a rule gets a new manifest id, as the manifest says.

| # | Review point | Resolution (no rule change) |
|---|---|---|
| 1 | Features end 2026-08-14 while the panel ends 2026-08-28; imputation unspecified | No imputation anywhere. Direction origins stop at 2026-08-13 by construction (`last_origin`). The structural family does not use `features_v2.parquet`; its features are computed from the panel bars alone, so its later `last_origin` (2026-07-29, the last origin with a full 21-session label) is not a mismatch. |
| 2 | Early stopping could flatter survivors | Stopping removes a candidate from further fits only; its blocks 1-3 results stay in the results, in the ledger and in the Holm family. Each candidate's skill is measured on its own outer blocks, so a stopped candidate cannot change a survivor's number. A stopped candidate can never be promoted. |
| 3 | Ablations in the multiplicity family but on different data | Ablations are full outer runs of the inner winner on the identical outer blocks and rows, so they share the data of every other family member. |
| 4 | Different `last_origin` per family | Deliberate: direction is bounded by the feature file, structural by the 21-session label horizon (panel end 2026-08-28 minus 21 sessions). Both are fixed before any fit. |
| 5 | Training window wording | Training rows are origins with calendar index `<= test_start_index - 1 - h - embargo` (inclusive), units are sessions; this is exactly what `splits.outer_splits` and `splits.inner_folds` compute and selfcheck. |
| 6 | Abstention threshold re-estimation | The threshold is recomputed inside every outer block from that block's own inner-fold out-of-sample predictions; nothing from the test block enters it. |
| 7 | Live DB mutability | No model in this campaign reads the live database. Fits read the hashed parquet snapshots only; the live database is read only by the audit scripts, whose outputs carry `generated_at_utc`. |
| 8 | Delisted labels | An origin without a bar at `i+h` has no label and is excluded from fitting and evaluation; no substitute price is ever used. The excluded share is published (`label_missing_share`: 0.05% at h=1, 0.26% at h=5 on the built dataset). |
| 9 | B0 tie days | Rows whose prequential-majority guess is undefined (tie or empty prior) are removed from the matched-row set for both the model and the baseline when computing `skill_pp`; the model's unmatched accuracy is reported separately as `acc`. |

Built dataset (from `out/direction_dataset_v1.json`): 1,082,271 eligible rows, 1,664 origin
sessions 2019-12-30..2026-08-13, 1,102 symbols, 49 feature columns, input hashes equal to the
manifest's.
