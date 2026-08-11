# Pre-registration: is the cross-sectional signal tradeable?

Written and committed BEFORE the results were seen. That is the whole point of
the file: "push it to its limit" is the request that manufactures edges, and the
defence is declaring the grid first and reporting every cell of it.

## What is already established

`research/XSDIRECTION_FINDING.md`: the relative target scores +1.83pp over a 50%
null, CI [+1.35, +2.22], 5 of 5 years positive, noise control at -0.01pp.

That is a HIT RATE on a median split, and it ignores magnitude. Being right on
small moves and wrong on large ones scores well there and earns nothing.

## The grid, declared in advance

Four cells. Every one is reported, including the ones that fail.

| | hold=1 | hold=5 |
|---|---|---|
| all symbols | cell A | cell B |
| top 1000 by 21d dollar volume | cell C | cell D |

Both axes are chosen a priori, not from inspection:

- **hold=5** because a one-day book turns over daily and cost is proportional to
  turnover. Lengthening the hold is the standard remedy, decided before any cost
  number was computed.
- **liquidity filter** because spread and impact are worst in the smallest
  names, so a signal that lives only in microcaps is not tradeable. This is the
  usual place a cross-sectional result dies.

## Metrics, and what counts as success

- Mean daily Spearman **IC** with a 95% CI over days.
- **Decile spread**: top decile minus bottom decile forward return, per day, in
  basis points, 95% CI over days.
- **Turnover**, and spread **net of 5 / 10 / 20 bps per side**.
- Noise control (labels shuffled within day) on the headline cell.

SUCCESS = net spread positive with a CI excluding zero at 10 bps per side, in a
majority of walk-forward years. FAILURE = anything less, and in particular a
positive gross spread that costs turn negative. A gross number quoted without
its cost is not a result.

## What is not being done

No feature is added, no hyperparameter is tuned, no year is dropped. The model
is exactly the one already committed. If all four cells fail, the answer is that
the signal is real and not tradeable, and that is the finding.
