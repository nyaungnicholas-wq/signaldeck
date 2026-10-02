# Step 10 readiness notes
Dated 2026-10-02. Docs only; nothing here is built.

## (a) Connect Alpaca: paper mode with member-written rules

### Blockers
1. Executing in members' accounts from member-written rules may make SignalDeck an investment adviser or a commodity trading adviser (CTA); the research brief flagged Terry's Tips (2005) as the precedent to check. Counsel must review before any build.
2. Alpaca's terms for a commercial app and its OAuth (Connect) program need written approval from Alpaca.
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
- Apply to Alpaca and get written approval.
- Decide whether a separate surface is wanted at all.
- First have step 7 run one committed paper session of its own book.


## (b) Own futures via IBKR micros

### Blockers
- An IBKR account (owner).
- IBKR API / TWS or IB Gateway setup, which needs the owner's credentials and is never done by an agent.
- CME market data licence costs (the brief: CME bars need a paid licence even when delayed).
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