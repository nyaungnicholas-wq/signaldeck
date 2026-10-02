# Publisher guardrails

SignalDeck's member product is meant to stay on the publisher side of the line
between publishing research and giving personalised investment advice. These
are the rules it keeps, and what enforces each one. Changing a rule means
changing its test in the same commit, with the reason.

Not legal advice; counsel review pending. This file records engineering
controls, not a legal conclusion.

| # | Rule | Enforced by |
|---|------|-------------|
| 1 | **Impersonal.** Every member reads the same forecasts. Nothing a member reads about a symbol depends on who they are, what else they watch, or their settings. | `TestMemberForecastsAreImpersonal` (daemon/internal/api/guardrails_test.go): two members with different watchlists and alert settings get byte-identical bodies from every member-reachable GET, except the listed per-user routes (own watchlist, own identity, own alert switches). `TestComposeIsImpersonal` (daemon/internal/memberdigest) does the same for the daily email. |
| 2 | **Same forecasts for all, including in email.** The daily read is a filtered view of the public forecast table, worded identically for everyone, and says so in its footer. | `memberdigest.Compose` builds each symbol's block from the symbol and the shared forecast rows only; footer text pinned by `TestComposeFlipsCryptoAndFooter`. |
| 3 | **No account or portfolio inputs.** No member route accepts capital, position size, portfolio, holdings, risk tolerance, balance, equity, quantity or leverage. | `TestMemberRoutesTakeNoPersonalInputs` (guardrails_test.go): a source scan of every handler a member session can reach, pinned to an allowlist (`memberInputs`); a parameter whose name matches those words fails outright, an unlisted one fails until listed. |
| 4 | **No personal sizing.** Members are never told how much to buy or sell. | Rule 3 removes the inputs sizing would need; position, portfolio and paper-order routes are admin-only (`memberRoutes` allowlist in daemon/internal/api/accounts.go; `TestMemberSurfaceIsPinned`). |
| 5 | **No execution for members.** Members cannot place, route or simulate orders, and nothing they do starts ingestion. | Member tier gate (security.go step 6b, `memberAllowed`); `neverMember` list and `TestMemberRoutesServeNoLicensedData` (membersurface_test.go) keep subscribe/unsubscribe and order paths off the member surface. |
| 6 | **Hypothetical-performance labels.** Any backtest or paper number shown to a member says it is hypothetical and not a promise of results. | `TestMemberForecastsAreImpersonal` also requires every `historicalAccuracy` on the member's `/api/regimes` and `/api/vol-regime` to carry `"evidence":"backtest"` (stored vol forecasts lost that label until `volregime.Labelled`); the email footer says "backtest accuracy is hypothetical and not a promise of results" (`TestComposeFlipsCryptoAndFooter`). |
| 7 | **Describe the model literally.** The model is statistical classifiers trained on price and volume history. It is not an AI adviser and is never described as one. | Email footer line pinned by `TestComposeFlipsCryptoAndFooter`; the LLM summary never runs for a member (`/api/company/profile` member branch, membersurface_test.go). |
| 8 | **Derived data only; no crypto for members.** Members see forecasts, never vendor prices, volumes, returns or headlines; crypto is not part of the member product. | `TestMemberResponsesCarryNoVendorSentinels` (membersentinel_test.go) for every member route; `TestDigestCarriesNoVendorSentinels` for the daily email. |
| 9 | **Opt-in delivery.** Nothing is emailed or messaged unless the member turned it on; every email carries a one-click unsubscribe. | `member_alert_prefs` defaults off (`TestMemberAlertPrefs`); `TestUnsubscribeLinkIsAnonymousAndTokened`; `TestWorkerSendsOncePerDayAndHonoursUnsubscribe`. |

## When you add a member feature

1. If it reads a new request parameter, add it to `memberInputs` with what it is.
   If you are tempted to add one that describes the member's money, stop: that is
   the line this file exists to hold.
2. If its body differs per member, it belongs in `perUserRoutes` only if it
   serves the member's own settings or list, never a forecast or a ranking.
3. Any number with "accuracy", "return" or "performance" in it needs its
   hypothetical/backtest label in the same payload or message.
