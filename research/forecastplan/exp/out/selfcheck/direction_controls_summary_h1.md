# Horizon 1
kind | model | feature_set | transform | pooled acc | B0 acc | skill_pp | CI21 lo | CI21 hi | p_le_0
--- | --- | --- | --- | --- | --- | --- | --- | --- | ---
control | M1_c0.1 | all | C1_shuffle_within_day | 0.5261 | 0.4970 | 2.81 | 1.0713 | 4.4611 | 0.0005
control | M1_c0.1 | all | C2_lag21 | 0.4954 | 0.4970 | 0.00 | -1.8891 | 1.0218 | 0.6915
control | M1_c0.1 | all | C3_noise | 0.5012 | 0.4970 | 0.50 | -1.0816 | 1.5181 | 0.3295
ablation | M2_hgb_shallow | no_market | none | 0.7181 | 0.4970 | 22.09 | 20.9226 | 22.9266 | 0.0000
ablation | M2_hgb_shallow | cross_sectional_only | none | 0.4979 | 0.4970 | 0.10 | -0.5886 | 0.6283 | 0.4930
ablation | M2_hgb_shallow | momentum_only | none | 0.4961 | 0.4970 | -0.11 | -0.8036 | 0.6747 | 0.6020
ablation | M2_hgb_shallow | all | none | 0.7180 | 0.4970 | 22.10 | 20.9422 | 22.9167 | 0.0000
