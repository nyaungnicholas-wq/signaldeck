# Audit note — accuracy impact of retiring FINRA, Cboe put/call and FRED

**Date:** 2026-10-22 (run 06:09 UTC). **Raw output:** [2026-10-22-licence-retire-ablation.txt](2026-10-22-licence-retire-ablation.txt).

**Keys ablated (22):** `short_vol_z`, `short_int_dtc` (FINRA), `pc_total` (Cboe),
`vix_level`, `vix_regime`, `vix_high_vol` and the 16 `macro_<series>_pct/_chg`
keys (FRED). They are the keys `gbm.RetiredSourceKey` now drops from training.

**Harness:** `daemon/cmd/selfref-ablation -keys <the 22> -versions 12`, on a
read-only `VACUUM INTO` snapshot of the live DB (sha256 `3f9166dc…`). "With" is
the pre-retirement layout, "without" is what ships.

| leg | with | without | Δ (without − with) | admitted (with → without) |
|---|---|---|---|---|
| GBM v12 1d (10 legs) | −0.0939 | −0.0944 | −0.0005 | 3 → 4 |
| GBM v12 1w (83 legs) | −0.1448 | −0.1901 | −0.0453 | 20 → 20 (20 legs flip the gate) |
| alphax 1d (pooled) | +0.0243 | +0.0386 | +0.0143 | admitted either way |
| alphax 1w (pooled) | refused | refused | — | refused either way (too few distinct days) |

Summary line: GBM 1d meanLift −0.094 without (10 legs, admitted 3→4), 1w
−0.190 without (83 legs, 20→20, mean lift 0.045 lower), alphax 1d admitted and
1w refused either way.

**Caveats.** Mean lift is negative in both arms: these legs carry no edge with
or without the retired keys. The 1w leg loses 0.045 of mean lift, but the
admitted count does not move. The snapshot is `3f9166dc`, not the `8e84953c`
snapshot behind the TradingView/StockTwits ablation, so the two results are not
directly comparable. The retirement is a licence decision, not an accuracy one.
