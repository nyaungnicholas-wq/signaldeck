# Horizon 5
kind | model | feature_set | transform | pooled acc | B0 acc | skill_pp | CI21 lo | CI21 hi | p_le_0
--- | --- | --- | --- | --- | --- | --- | --- | --- | ---
control | M1_c0.1 | all | C1_shuffle_within_day | 0.6191 | 0.5069 | 11.15 | 11.7922 | 13.8328 | 0.0000
control | M1_c0.1 | all | C2_lag21 | 0.5055 | 0.5069 | -0.08 | 0.3373 | 2.3514 | 0.0070
control | M1_c0.1 | all | C3_noise | 0.5022 | 0.5069 | -0.48 | -0.1193 | 2.7812 | 0.0390
ablation | M2_hgb_shallow | no_market | none | 0.7145 | 0.5069 | 20.77 | 20.6250 | 23.7698 | 0.0000
ablation | M2_hgb_shallow | cross_sectional_only | none | 0.5129 | 0.5069 | 0.55 | 0.8333 | 3.0657 | 0.0000
ablation | M2_hgb_shallow | momentum_only | none | 0.4968 | 0.5069 | -0.96 | -0.6649 | 1.5841 | 0.2295
ablation | M2_hgb_shallow | all | none | 0.7145 | 0.5069 | 20.77 | 20.6044 | 23.7999 | 0.0000
