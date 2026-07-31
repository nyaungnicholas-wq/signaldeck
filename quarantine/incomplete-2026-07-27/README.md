# Quarantined incomplete work — 2026-07-27

These files were written by the score-loop workflow but were never compilable:
each references symbols that were planned and never implemented. They were
untracked, so neither CI (which builds `git archive HEAD`) nor
`ops/manifest-check.sh` (which follows tracked markdown) could see them, yet
they broke `go build ./...` for every developer working in the tree.

They are moved here rather than deleted — the intent behind each is real and
worth finishing. Nothing here is compiled.

| File | Intent | Missing before it can return |
|---|---|---|
| `journal.go` | Make worker-run bookkeeping unlosable. Documents a measured live defect: 300+ `worker_runs` write-deadline expiries per day against the single `SetMaxOpenConns(1)` write connection, each leaving a run stuck at `running` and under-counting the multiplicity divisor into a *looser* correction. | A `jrnl` field on `workers.Runner`, plus wiring the async journal into the run lifecycle. |
| `buildattest.go` | Attest that a published build's discovery protocol matches the pre-registered one. | `store.PreregDiscoveryProtocols`, `prereg.StrictestDiscoveryProtocol`, a `discoveryProtocol` type, and `researchx.DiscoverConfig.Intent` / `researchx.IntentReplay`. |
| `ledgerrecon.go` | Reconcile the measured ledger gap against the stored one. | `store.MeasureLedgerGap`, `store.StoredLedgerGap`, `store.LedgerGapMetaKey`. |

Their `_test.go` companions are quarantined alongside them for the same reason.

Note the `journal.go` defect is real and still running in production: the fix
is quarantined, the bug is not.
