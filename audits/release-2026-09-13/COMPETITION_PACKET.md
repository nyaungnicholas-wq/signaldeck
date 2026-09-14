# Competition packet — DRAFT for Nicholas, not submitted

**Nothing here has been submitted, uploaded or sent.** This is material to review,
correct and then use. Every fact about *you* is a placeholder, because an agent
cannot establish it — see "What only you can answer".

## Which competition — UNRESOLVED

The instruction did not name one. Repository documents mention two:

| Event | Status |
| --- | --- |
| **Congressional App Challenge (CAC)** | Used as the **provisional** packaging target, per instruction. |
| Beginner's Paradise — FirstCommit | Separate eligibility question, **not** an engineering task. `https://firstcommit.devpost.com/rules` |

**[NICHOLAS TO CONFIRM] which event, if either.** The packet below is shaped for
CAC and would need rework for FirstCommit.

## Official CAC rules as rechecked 2026-09-14

Sources: [2026 CAC Rules PDF](https://www.congressionalappchallenge.us/wp-content/uploads/2026/05/2026-CAC-Rules.pdf) ·
[Rules page](https://www.congressionalappchallenge.us/students/rules/)

| Rule | Value | Confidence |
| --- | --- | --- |
| Deadline | **2026-10-26** | stated on the rules page |
| Eligibility | middle or high school student, **US resident at submission** | stated |
| District | district of residence **or** of school attendance; one district only | stated |
| Teams | up to 4; at least half eligible for that district | stated |
| Demo video | **1–3 minutes**; over/under may be penalised at judges' discretion | stated |
| Video hosting | YouTube or Vimeo, set to **public** | stated |
| AI assistance | **permitted, provided all AI usage is fully disclosed** in submission materials | stated |
| Pre-existing projects | **NOT ESTABLISHED.** Could not be extracted from the official rules in this pass. | **unresolved** |

> The pre-existing-project question is the one that matters most here and it is
> the one I could not answer. SignalDeck was not started for this event. If the
> rules exclude prior work, no amount of engineering makes it eligible, and
> relabelling it as new work is not an option. **Read the rulebook section on
> this before anything is submitted.**

## What only you can answer

Do not let anyone, including me, fill these in.

- `[NICHOLAS TO CONFIRM]` Your grade/school and Congressional district, and that
  the district is participating this cycle.
- `[NICHOLAS TO CONFIRM]` Whether a pre-existing project is admissible, and
  whether SignalDeck counts as one.
- `[NICHOLAS TO CONFIRM]` Team: solo or named teammates, and their eligibility.
- `[NICHOLAS TO CONFIRM]` What you personally designed, wrote, debugged, tested
  and decided, versus what was AI-assisted — in your own words. The AI-disclosure
  draft below deliberately leaves this blank.
- `[NICHOLAS TO CONFIRM]` What sparked the idea.
- `[NICHOLAS TO CONFIRM]` What you learned and what you would do differently.

**No first-person accomplishment sentences have been written for you anywhere in
this packet.** A successful overnight agent pass cannot establish a student's
contribution, and inventing one would be the kind of claim this whole project is
built to refuse.

---

## One-sentence purpose

> SignalDeck is a market-forecasting instrument that publishes its own report
> card: every prediction is written down and hash-chained before the outcome
> exists, then graded in public against a baseline registered in advance —
> including, and especially, the results that failed.

**Audience:** anyone who has seen a trading service advertise a win rate they
cannot check. The product is the verification, not the forecast.

---

## Demo script — target 2:45, inside the 1–3 minute limit

Rehearse with a stopwatch. Timings are the budget, not a suggestion.

### 0:00–0:20 — the problem, concretely

> "Every signal service online advertises a win rate. None of them let you check
> it. Nothing stops anyone publishing a prediction after the fact, or quietly
> deleting the ones that were wrong."

### 0:20–0:55 — the landing page, *including the red banner*

Open `/`. **Do not hide the refusal.** Read it aloud:

> "Right now this site is refusing to publish its own accuracy figures. 18 of
> the 76 graded day-horizons had a collapsed cross-section — the model gave
> almost the whole market the same probability, so those rows are one
> market-wide call repeated 328 times, not 328 independent forecasts. So the
> numbers are withheld. That refusal is the feature."

This is the strongest 35 seconds available and most entrants have nothing like
it. A judge has never seen a submission refuse to show its own score.

### 0:55–1:40 — the receipts (`/proof`)

Show the ledger panel and read the scoping *from the screen*:

- 501,000+ entries, chain intact
- verification mode: **incremental** — the rows after the last checkpoint
- anteriority proven through entry **#500,887**
- **624 entries carry no anteriority proof**

> "It does not claim to be tamper-proof. It tells you exactly how far the proof
> reaches and where it stops. An edit anywhere breaks the chain — but proving
> *when* something was written needs a signature published outside this machine,
> and it says which of the two you're looking at."

Then scroll to the pre-registration chain: the claim, the date, the digest, and
whether it was frozen before anything could be graded.

### 1:40–2:15 — one real interaction

Sign in, open a symbol's research page, change the chart timeframe. Keep it
short: **one complete, working interaction beats a tour of twelve pages.**

### 2:15–2:35 — the volatility record (`/volatility`)

> "This is the forecast that's still accruing. Both horizons say INSUFFICIENT —
> 5 of the 60 trading days needed at one day ahead. It won't give a verdict
> until it has the evidence it registered in advance that it would need."

### 2:35–2:45 — close

> "Most of what this does is decide what it is *not* allowed to say."

### Do not show

- Any operator/admin page, `/lab/system`, health dashboards
- Anything with an email address, credential, or local file path
- The `hud` route or personal paper positions
- Any number currently withheld — reading one aloud undoes the whole argument

---

## Known limitations — say these before a judge finds them

Leading with these is the credible move, and it is consistent with the product.

1. **No directional predictor here has ever beaten its own baseline.** Some were
   retired on measured evidence, some are indistinguishable from chance, some are
   withheld because the window is degenerate. Three different states.
2. **The volatility forecast has no verdict yet** — INSUFFICIENT at both horizons.
3. **The paper book is a simulation.** No broker, no money, no claim that a real
   fill would have cost the same.
4. **Anteriority is partial** — 624 entries beyond the newest anchor have
   edit-detection only.
5. **It is not advice** and has no per-symbol risk number.

---

## Technical Q&A — learn these, don't read them

A judge may ask. You should be able to answer in your own words.

**"What is a collapsed cross-section?"**
On some days the model gave nearly every stock the same probability — 5 or 6
distinct values across 328 symbols. Counting that as 328 independent forecasts
inflates the sample enormously when it is really one market-wide call. Those days
are detected and the figures over that window are withheld.

**"Why hash-chain the predictions?"**
Each entry includes the hash of the previous one, so editing, deleting or
reordering any row breaks the recomputation from that point on. It makes an edit
*detectable*. It does not by itself prove *when* a row was written — that needs a
signature published somewhere the operator does not control.

**"What stops you deleting the bad predictions?"**
The chain breaks if I do. And periodically a digest of the chain head is signed
and published externally, so history before that point cannot be rebuilt without
also reproducing the signature.

**"Why does it refuse to show accuracy?"**
Because the window it would be computed over contains days that cannot support
the claim. Publishing anyway would be the exact thing the project exists to
refuse.

**"What's QLIKE?"**
The loss function the volatility forecast is scored under, registered before any
of it was measured. Lower is better. It measures forecast error — **not** money.

**"How do you know it isn't overfitted?"**
The claim, the baseline, the loss function and the minimum evidence were all
frozen and hash-chained before the forecasts existed, so the bar cannot be moved
after seeing results.

---

## AI-disclosure draft — CAC requires full disclosure

Rules recheck: AI tools are permitted **provided all AI usage is fully
disclosed**. This draft is deliberately incomplete where only you can speak.

> **AI assistance disclosure**
>
> I used AI coding assistants during the development of SignalDeck. Specifically,
> AI assistance was used for: `[NICHOLAS TO CONFIRM — list the areas honestly:
> e.g. code review, refactoring, test writing, documentation, debugging]`.
>
> The following were my own work: `[NICHOLAS TO CONFIRM — the design decisions,
> the research questions, the statistical methodology choices, what to build and
> why, and which results to trust]`.
>
> `[NICHOLAS TO CONFIRM]` I can explain how every component of this project works
> and why it was built the way it is.
>
> The project's scientific conclusions — including the retirement of the
> directional model and the current refusal to publish accuracy figures — follow
> from pre-registered rules, not from any judgement made by an AI assistant.

**Do not sign the last line unless it is true.** If there are parts you cannot
yet explain, the honest move is to learn them before submitting — the Q&A above
is the place to start.

---

## Screenshot / recording checklist

- [ ] Use the **final verified build**, not an earlier screenshot
- [ ] Desktop 1280px and mobile 375px both captured
- [ ] Refusal banner **visible**, not scrolled past
- [ ] No email address, credential, token or `C:\Users\...` path on screen
- [ ] Browser profile signed out of anything personal; no bookmarks bar
- [ ] No operator/admin route in frame
- [ ] Paper activity, if shown, labelled as simulated **on screen**
- [ ] Video 1–3 minutes, public on YouTube or Vimeo
- [ ] Watch it back once before submitting

**No recording was made.** Screen capture with narration is yours to do — and
the narration has to be yours.

---

## Credits

- Market data: per `DATA_SOURCES.md` — **[NICHOLAS TO CONFIRM]** each provider's
  current terms permit the outputs shown publicly.
- Stack: Go daemon, Next.js 16 web, SQLite.
- AI assistance: as disclosed above.
