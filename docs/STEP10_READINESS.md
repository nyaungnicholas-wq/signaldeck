# Step 10 readiness notes
Dated 2026-10-01. Docs only; nothing here is built. Items marked **(to verify)** are
working assumptions from the research brief, not checked facts.

## (a) Connect Alpaca: paper mode with member-written rules

### Blockers
1. Executing in members' accounts from member-written rules may make SignalDeck an investment adviser; the research brief flagged Terry's Tips as the precedent to check **(to verify: likely SEC v. Terry's Tips, D. Vt. 2006, not 2005)**. Counsel must review before any build. The commodity trading adviser (CTA) framing fits futures signals (part b), not equities paper trading; for equities the question is the investment-adviser line.
2. Alpaca's terms for a commercial app and its OAuth (Connect) program **(to verify: the OAuth program appears to be registration-based, so "written approval" may mean registering the app and accepting its terms rather than a letter)**.
3. Step 7's execution layer has not yet run a committed paper session of SignalDeck's own book, so there is no proof it executes correctly.
4. The publisher guardrails forbid personal inputs, so this must be a separate product surface with its own guardrails, not a member route.

### Safe stage 1
- Paper accounts only.
- Member's own rules only.
- No SignalDeck signal or forecast as an input to any rule.
- No live-money switch in the code.
- Per-member isolation.
- A kill switch.
- Every order logged.

### Owner decisions needed
- Retain counsel and get a written opinion.
- Register with Alpaca's OAuth program and confirm what approval it actually requires (to verify).
- Decide whether a separate surface is wanted at all.
- First have step 7 run one committed paper session of its own book.


## (b) Own futures via IBKR micros

### Blockers
- An IBKR account (owner).
- IBKR API / TWS or IB Gateway setup, which needs the owner's credentials and is never done by an agent.
- CME market data licence costs **(to verify: the brief says CME bars need a paid licence even when delayed; the fee depends on display vs non-display use vs redistribution, so the use must be named first)**.
- Step 7's execution layer is Alpaca-only and not live-proven.
- The signal freeze (no new forecast or grader change while frozen).
- No futures strategy has passed a pre-registered test.

### Order of steps
1. Pre-register a futures strategy and pass its test on licensed data.
2. Owner opens the IBKR account and buys the CME data licence.
3. Owner installs and signs in to IB Gateway (paper).
4. Add an IBKR adapter to the execution layer behind the same interface, paper only.
5. Run committed paper sessions and grade them.
6. Only then a live micro contract at minimum size, owner's decision.

Neither (a) nor (b) is started; each needs the owner decisions above first.