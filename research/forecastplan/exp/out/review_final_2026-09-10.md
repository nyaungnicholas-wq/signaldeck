error: (429) Too Many Requests.
1. **Verdict per family & MPUI**  
   - Direction h1: no row reaches skill_pp ≥ 1.0 pp; best = 0.3656 pp (M1_c1 none). **Verdict:** no evidence of useful skill; MPUI not met.  
   - Direction h5: max skill_pp = 0.0581 pp (M4_market none). **Verdict:** no evidence; MPUI not met.  
   - Trend21|screen: max skill_pp = 0.0041 pp (M2_hgb_shallow transition). **Verdict:** null; MPUI not met.  
   - Trend21|noscreen: max skill_pp = 0.0026 pp (M1_c0.1 class). **Verdict:** null; MPUI not met.  
   - Liquidity21|screen: M2_hgb_shallow transition skill_pp = 1.0249 pp, CI63 (0.2646, 1.8046), blocks≥1pp = 3. **Verdict:** positive point estimate but fails block‑count rule; MPUI not met.  
   - Liquidity21|noscreen: same model skill_pp = 0.6803 pp, CI63 (0.2392, 1.1611), blocks≥1pp = 1. **Verdict:** weaker; MPUI not met.  
   - Vol21|screen: best skill_pp = 0.8117 pp, CI63 (‑1.1974, 2.6235) → lower bound ≤0. **Verdict:** inconclusive; MPUI not met.  
   - Vol21|noscreen: best skill_pp = 1.0809 pp, CI63 (‑0.3167, 2.1496) → lower bound ≤0. **Verdict:** inconclusive; MPUI not met.  
   **Overall:** no row in any family satisfies the pre‑frozen MPUI (≥1.0 pp, CI > 0, and ≥4/6 blocks with skill ≥ 1 pp, or the structural balanced‑accuracy alternative).
2. **Liquidity21|screen M2_hgb_shallow|transition**  
   - *For:* skill_pp = 1.0249 pp exceeds the MPUI threshold; CI63 (0.2646, 1.8046) excludes zero; p_le_0 = 0.0020 indicates very low chance of skill ≤ 0; grid7/grid14 skill = 0.79/0.76 shows strong subsample consistency; predicted transition share = 0.0703 (>0) supports the proposed mechanism.  
   - *Against:* blocks≥1pp = 3 < 4 required for MPUI; campaign‑wide multiplicity (48 rows) would further weaken significance (Holm p_adj = 0.0140 within table, likely non‑significant after 48‑row correction); unscreened version drops to skill_pp = 0.6803 pp, blocks≥1pp = 1, indicating fragility; predicted transition share remains low (7 %), questioning the mechanistic relevance.  
   - *Reading:* The point estimate and CI favor a real lead, but the insufficient block consistency and multiplicity penalty tilt the overall weight against treating it as a robust lead.
3. **Controls that weaken leakage detection**  
   - Horizon 5, C3_noise: skill_pp = 0.16 pp, CI21 (0.0000, 0.0000) – a zero‑width interval provides no variance, rendering the test impotent to detect leakage.  
   - Horizon 1, C3_noise: skill_pp = 0.15 pp, CI21 (‑0.0111, 0.0089) – extremely narrow, similarly limiting power.  
   - The lag21 controls (C2_lag21) show skill near zero with wide CIs, but they do not actively break potential leakage pathways as effectively as a proper noise or shuffle control should.
4. **Conditions for prospective shadow candidacy (without breaking the freeze)**  
   - The candidate must achieve MPUI: pooled skill_pp > 1.0 pp with CI lower bound > 0 **and** ≥4 of 6 blocks showing skill ≥ 1 pp (or, for structural, balanced accuracy ≥ persistence + 5 pp with accuracy within 0.5 pp of persistence).  
   - After adjusting for the full campaign family (48 rows) using Holm‑Bonferroni, the adjusted p‑value must be < 0.05 (or equivalently, the family‑wise error rate controlled).  
   - All control rows must exhibit |skill_pp| ≤ 1.0 pp **or** a CI that includes zero; any control with |skill_pp| > 1.0 pp and CI excluding 0 would void the candidacy.  
   - The candidate’s hyper‑parameters, feature set, and calibration must be fixed **before** the unfreezing point; no re‑tuning on the evaluation data is allowed.  
   - The lead must be evaluated on a truly out‑of‑sample slice that was not used in any inner‑fold or grid search during the frozen campaign.  
   - A plausible, pre‑specified mechanism (e.g., the predicted‑transition‑share logic) should be documented and shown not to be a post‑hoc justification.  
   - Re‑run the blocked bootstrap with the same block size (63 sessions for structural) to confirm the CI lower bound remains > 0 under the frozen data split.  
Only if **all** these conditions hold can the liquidity21|screen M2_hgb_shallow|transition be promoted to a prospective shadow candidate without violating the frozen protocol.
