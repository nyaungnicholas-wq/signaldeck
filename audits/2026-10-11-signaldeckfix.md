# /signaldeckfix 2026-10-11

Inspected HEAD 3bf3f704 (public-launch). Live daemon and web: ea1b17d5. The fixes below are uncommitted and NOT deployed.

| ID | Component | Finding | Status | Evidence |
|---|---|---|---|---|
| SF-01 | pipeline/signal8.go, maintain | The filings-poller re-inserted every EDGAR filing past the 180d retention horizon on each sweep (17k-45k rows every 2h). derived-retention then archived and deleted them again. The cold archive holds 7.49M rows but only 667k distinct accessions (about 11x duplication). Re-inserted old Form 4s used up the 25-per-run parse budget, which left 4,045 recent Form 4s unparsed and starved InsiderNetActivity. | FIXED (not deployed) | `maintain.FilingsRetention()` is now shared, and the poller skips filings with `FiledTs < cutoff`. `TestFilingsPoller_SkipsFilingsPastRetention` fails without the fix ("2 new rows ... form4 parsed 1") and passes with it. |
| SF-02 | pipeline/compositescorer.go | Both composite scorers hit the 30m deadline at 07:24Z, and two other slow windows ran 578-608s against a normal 33s. Setup reads slow down under maintenance bursts. The dominant cost is TVRatingSkill, a scan of 2.1M tv_ratings rows (8-16s) repeated every 10 min by each scorer. | FIXED (not deployed) | TVRatingSkill is memoized per worker for 6h and errors are never cached. `TestCompositeScorerMemoizesTVSkill` fails with the memo disabled. |
| SF-03 | ops/tasks | Check-Task-Health was red every day: the Quick Tunnel xml was enabled but the live task had been retired (Disabled) by the owner. | VERIFIED | `install-windows-tasks.ps1` report now shows `ok SignalDeck Quick Tunnel Disabled`. |
| SF-04 | ops/ledger-revisions.txt | Check-Grader-Health reported MANIFEST STALE (3 revisions 7+ days old, 12 unrecorded in total). | VERIFIED | `ledger-provenance.sh --diff` gives OK (recorded=161); `--check` gives all reachable. |
| SF-05 | web runtime | node_modules has sharp 0.35.4 while the lockfile pins 0.35.5 (GHSA-wq5f-xc86-pv6w, commit 3bf3f704). `npm audit` reads the lockfile and reports 0, which masks it. `/_next/image` is reachable (200), but inputs are local-only. | BLOCKED: production web release needs owner authorization | Fix: stop web, `npm ci`, `ops/web-release.ps1`. |
| SF-06 | Cloudflare zone | Cloudflare Web Analytics injects beacon.min.js, which the CSP blocks, producing a console error on every page. | BLOCKED: owner dashboard decision | Turn off Web Analytics for the hostname. Widening the CSP would need a privacy-notice change. |
| SF-07 | WAL | The WAL reached 1.0-1.2 GB and TRUNCATE kept returning BUSY during the 07:24Z burst. This is the known read-pressure shape (project_signaldeck_wal_starvation). SF-01 and SF-02 remove the two largest avoidable read/write loads. | NOT COMPLETE: re-measure after deploy | storage-governor detail lines |
| SF-08 | workers | forecast-monitor COVERAGE STARVED and expectancy-trainer benched | NOT APPLICABLE: honest gate outputs (known) | memory audit 08-25 |
| SF-09 | web lint | 2 exhaustive-deps warnings (Turnstile onToken, account router) | NOT APPLICABLE: the callbacks are stable setters/router | callers pass `setTurnstileToken` |

Accepted residuals (from the fresh-context review):
- After a restart, the first TVRatingSkill scan still runs under the run deadline.
- The tvrating gate can flip up to 6h late.
- The 1d and 1w scorers memoize separately.

Checks run: `go vet ./...` and `go test -count=1 ./...` are green before and after the fixes. Web: tsc 0, lint 0 errors, `npm test` 146/146. controls_evidence `--check` passes. The public site was opened in a browser.

Remaining archive cleanup: `data/archive/filings` needs a one-off dedupe by accession. It is a data write, so it was not done.
