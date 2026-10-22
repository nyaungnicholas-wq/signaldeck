# Audit note — accuracy impact of retiring FINRA, Cboe put/call and FRED

**Date:** 2026-10-22 (run 06:09 UTC). **Raw output:** [2026-10-22-licence-retire-ablation.txt](2026-10-22-licence-retire-ablation.txt).

**Keys ablated (22):** `short_vol_z`, `short_int_dtc` (FINRA), `pc_total` (Cboe),
`vix_level`, `vix_regime`, `vix_high_vol` and the 16 `macro_<series>_pct/_chg`
keys (FRED). They are the keys `gbm.RetiredSourceKey` now drops from training.

**Harness:** `daemon/cmd/selfref-ablation -keys <the 22> -versions 12`, on the
snapshot with sha256 `3f9166dc…`. That snapshot was an earlier `VACUUM INTO`
copy made by this harness. This run reused it and did not copy a database
again, and the file has since been deleted. This record therefore cannot show
which database the copy was taken from, and the run cannot be repeated on the
same file. The raw output's `source=C:/Program Files/Git/nonexistent-not-used.db`
line is the `-db` flag as passed: the tool was pointed at the existing snapshot
with a deliberately nonexistent source, which it prints but never opens when it
reuses a snapshot. "With" is the pre-retirement layout, "without" is what ships.

| leg | with | without | Δ (without − with) | admitted (with → without) |
|---|---|---|---|---|
| GBM v12 1d (10 legs) | −0.0939 | −0.0944 | −0.0005 | 3 → 4 |
| GBM v12 1w (83 legs) | −0.1448 | −0.1901 | −0.0453 | 20 → 20 (20 legs flip the gate) |
| alphax 1d (pooled) | +0.0243 | +0.0386 | +0.0143 | admitted either way |
| alphax 1w (pooled) | refused | refused | — | refused either way (too few distinct days) |

Summary line: GBM 1d meanLift −0.094 without (10 legs, admitted 3→4), 1w
−0.190 without (83 legs, 20→20, mean lift 0.045 lower), alphax 1d admitted and
1w refused either way.

**Caveats.** For the GBM legs, mean lift is negative in both arms: they carry
no edge with or without the retired keys. This does not apply to the alphax 1d
leg, whose mean lift is positive in both arms and admitted either way. The GBM
1w leg loses 0.045 of mean lift. Its admitted count stays at 20, but the
admitted set churns: 20 legs flip the gate, so the 20 legs admitted without the
keys are not the same 20 as before. The snapshot is `3f9166dc`, not the `8e84953c`
snapshot behind the TradingView/StockTwits ablation, so the two results are not
directly comparable. The retirement is a licence decision, not an accuracy one.
