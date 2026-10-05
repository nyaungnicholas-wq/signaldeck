# Claims reconciliation: release audit 2026-10-05

Each earlier status claim or product claim, the evidence checked on 2026-10-05 (UTC), and the corrected wording. "Retracted" means the claim must not be repeated.

## Earlier status claims (from the brief)

| Claim | Evidence (2026-10-05) | Verdict / corrected wording |
|---|---|---|
| "The live tree is clean." | `git status --short` empty at 1249c436, 02:39Z. | TRUE at audit start. |
| "`main` has the same files as `public-launch`." | 02:39Z: `main` (34f1533a) differed by one file, `docs/COMPETITION.md` (+6 lines), the content of PR #26. PR #26 merged at 02:53:58Z (merge f31f71e, 12/12 checks); afterwards the trees are identical. | TRUE again after PR #26. The claim was stale while #26 was open. |
| "No PRs are open." | PR #26 was open from 02:29:51Z to 02:53:58Z. | Superseded by #26, which the later update mentioned. Zero open PRs after 02:53Z. |
| "The anchors repository is public." | `gh repo view nyaungnicholas-wq/signaldeck-anchors` → PUBLIC. | TRUE. |
| "The daemon is running." | `signaldeckd` PID 60188 on 127.0.0.1:8322, revision 41a817a9 (binary stamp and `worker_runs.revision`). | TRUE. Runtime code equals HEAD 1249c436: nothing under `daemon/` or `web/src` changed between them. |
| "`/api/accuracy` returns 200." | 200 anonymously through the tunnel. | TRUE but not evidence of readiness. `/api/accuracy` also returned 503 ten times in the 24 h before the audit (AUD-19), and a 200 can carry a REFUSED status. |
| "The public site is at https://aim-logo-honest-hometown.trycloudflare.com." | Reachable at 02:47Z. The URL has rotated 5 times in 6.6 days. | TRUE at audit time. Any quick-tunnel URL is temporary (AUD-04). |
| "Enough 1d/1w data around mid-October." | Section 15 of PREREGISTRATION.md requires 10 credible days and the owner's approval. At audit time 7,123 1d and 5,499 1w calls were issued in the window and none had resolved. 1w needs ten non-overlapping 7-day blocks. | **Retracted** as a date. 1d can qualify about ten trading sessions after resolutions begin, at the earliest. 1w needs roughly ten weeks or more. Both then need the owner's yes. One switch covers both horizons, so lifting it on the 1d condition would publish 1w point figures with no interval. |

## Product claims corrected in this pass (branch audit-1005)

| Where | Was | Problem | Now |
|---|---|---|---|
| Landing ticker | "No figure without an interval" | The table under it shows point accuracies whose intervals are withheld. | "No skill claim without an interval" |
| Landing stat | "days of live evidence (longest record)" | The value is distinct graded days, which is not independent evidence (3 non-overlapping blocks). | "distinct graded days (longest record)" |
| Landing receipts link | "Recompute the whole chain yourself →" | The page runs an incremental daemon check; the full walk is operator-only. | "See what the daemon re-checked, and how to verify the anchors yourself →" |
| Landing constraints | "There is a paper book under Lab…" | Visitors and members cannot reach Lab. | The paper book is in the operator's private workspace and is not available to visitors or members. |
| Waitlist | "stored on this server only … reply to any message from us or contact the operator" | Database copies go to a private off-site repository; no message has been sent and no contact exists. | Says the address is kept in the database and its backups, on this machine and in a private off-site copy. The removal sentence is dropped until a contact exists (AUD-05). |
| Sign-up CTA | "never send anything but the record" | Account mail (confirmation, reset, "already has an account") and the opt-in digest exist. | Lists those emails. |
| Sign-in page | "a private workspace — sign in to open your dashboard, signals, and research" | Members cannot open the operator dashboard. | "Sign in to your SignalDeck account: your watchlist, regime reads and call journal. The public record needs no account." |
| Receipts page | "Open the full workspace →" (`/dashboard`) | Operator-only; every visitor bounced. | Links to the volatility record. |
| `/today` header | "validated reads, each with the accuracy it has actually measured" | The figures are backtest accuracies. | "regime reads, each with the accuracy it measured in backtests" |
| `/accuracy` | Retired flagship's 1d/1w figures (from a constant) | SD-30 withholds every 1d/1w directional figure; the page's own refusal path said they were not published. | Verdict and date only, with the reason the figures are withheld. |
| Unknown symbol | "This is on our side, not yours … Retry" | The cause is the reader's input. | "SignalDeck does not track this symbol." No Retry. |

## Claims still standing that this audit could not substantiate (left as is, flagged)

- "Retired models stay retired": the live `/api/accuracy` shows no retired rows (AUD-12). The retirement is on `/accuracy` and on the chain.
- "VALIDATED SIGNAL" badges on member pages describe backtest-only reads (AUD-37).
- README status table (2026-09-08) and the COMPETITION.md demo script predate SD-30; not edited here.
- Public `/api/track-record` regime intervals the registry withholds (AUD-11).
