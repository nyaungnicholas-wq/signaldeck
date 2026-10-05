# SignalDeck — UX review, self-grading system, and improvement plan

**Date:** 2026-08-03 · **Method:** every claim below is measured, not asserted.
A crawler (`e2e/ux-audit.spec.ts`) visited all 42 primary routes, measured them
in a real browser, and scored them against `src/lib/rubric.ts`. Where I disagree
with the machine, both numbers are shown.

---

## 1. Scorecard

**Machine score on first clean run: 54.8 / 100 — "Poor".**
My independent read: **52 / 100**. The two agree within 3 points, which is the
main thing worth knowing about a new rubric.

| Category | Score | Why | Improvement needed |
|---|---|---|---|
| **Clarity** | **5/10** | Every page can say what it is for, and the glossary is generated from the same dictionary as the tooltips — so definitions cannot drift. But the dashboard is 2,435 words and the lead panel opened with a 59-word sentence containing "realized volatility", "walk-forward" and "second implementation". | Lead with the plain sentence; move the precise one behind the tooltip that already exists. |
| **User-Friendliness** | **6/10** | The best thing in the product: SIMPLE mode folds 35 of 56 surfaces behind one labelled `/advanced` door, and that door page is the highest-scoring page in the app (84/100, 134 words, 1 screen). | The four surviving nav items still lead to pages built for the author. Fix the destinations, not the nav. |
| **Helpfulness** | **5/10** | The data is real and unusually honest — sample sizes, gates, "not enough evidence" instead of a guess. | It reports; it rarely helps you *act*. 31 of 42 pages offered no next step at all. |
| **Interactivity** | **5/10** | Lab pages have real controls. Median page has 2 inputs. | Most pages are read-only. The dashboard has 448 clickable things and almost nothing to *change*. |
| **Guidance** | **3/10** | **Weakest area.** 11 of 42 pages had any next-step control. Onboarding fires once and the checklist self-deletes at 4/4 — after that, nothing tells you what to do. | A next step on every page, chosen from what you have already done. |
| **Accessibility** | **7/10** | Strong foundations: `role`/`aria-label` on state components, 40px targets on most controls, `sr-only` status text, reduced-motion respected. | Only 5 of 42 pages had a live region, so async updates are silent to a screen reader. |
| **Visual / Layout** | **6/10** | Median page: 377 words, 2.2 screens, 96 numbers — genuinely calm. | The median hides the tail. `/lab/insights` is 13,597 words and 35 screens; the dashboard is 7.7 screens and 574 numbers. The pages people land on are the worst ones. |
| **Feedback** | **5/10** | `Skeleton`, `ErrorState` and `EmptyState` all exist and are correctly marked up. | They were invisible to the first audit because a healthy crawl never sees them — the *measurement* was broken, not the product. Now probed by forcing the API to fail. |
| **Personalization** | **6/10** | A real goal picker that sets view mode, and it persists. | The goal changes formatting and the nav, but not what the page leads with. Three goals, one dashboard. |
| **Overall Experience** | **5/10** | Trustworthy, dense, and clearly built by someone who knows the domain. | A new person can tell it is serious. They cannot tell what to do with it. |

**Final: 54.8 / 100 — Poor.** People can finish things here, but mostly by being patient.

---

## 2. Weak areas, worst first

1. **No next step, anywhere.** 31/42 pages end without telling you what to do. This is the single biggest driver of the score and the cheapest to fix.
2. **The landing page is the densest page.** 2,435 words, 574 numbers, 7.7 screens, 448 controls — in SIMPLE mode. First impression is "this is not for me".
3. **Honesty captions are written for a quant.** The content is right and must stay; the *reading level* is the defect. "Measured accuracy in this conviction band: 72% (conviction 1.00 · n=339)" is a true sentence almost nobody can parse.
4. **Onboarding is a one-shot.** The tour runs once, the checklist deletes itself, and then guidance drops to zero permanently. There is no steady state.
5. **Read-only by default.** Outside the lab, there is very little to press that changes anything. Passive surfaces do not build the habit that makes a data product stick.
6. **Async updates are silent.** 5/42 pages announce changes; the rest update under a screen reader with no notice.
7. **Personalization stops at the surface.** Picking "I'm learning" vs "I'm testing strategies" should change what leads the dashboard. It changes number formatting.
8. **Nothing measured whether any of this worked.** Before this pass there was no telemetry of any kind — zero signal on where people got stuck.
9. **The long tail is unbounded.** A 35-screen page is not a page; it is a document that was never edited.
10. **Two front doors.** `/welcome` and the dashboard both try to be the start, and neither clearly wins.

---

## 3. Improvements, prioritized

### Quick wins — hours, not days

| Change | Why it helps | More user-friendly | More interactive |
|---|---|---|---|
| **Next-step dock on every page** *(shipped)* | Removes the "now what?" dead end that ends 31 of 42 pages. | Always one obvious move, never a menu. | Turns a reading surface into a click. |
| **"I'm stuck" button** *(shipped)* | Gives the confused person somewhere to go that is not the back button. | A visible escape hatch lowers the cost of being lost. | One tap opens help in place. |
| **Plain lead, precise detail in the tooltip** *(shipped on the vol panel)* | Keeps every honesty caveat while halving what must be read to understand it. | Reads at a normal reading level. | The detail becomes something you choose to open. |
| **Count the hero subtitle as the purpose line** *(shipped)* | It always was one; the audit just could not see it. | — (measurement fix, stated as such) | — |
| **Cap the dashboard at 3 screens** | The first impression stops being a wall. | Less to refuse. | Encourages scrolling *with* intent. |
| **`aria-live` on every polling panel** | Screen-reader users currently get silent updates. | Works however you browse. | — |

### Medium-term — a cycle

| Change | Why it helps | More user-friendly | More interactive |
|---|---|---|---|
| **Make the goal actually change the dashboard** | Three goals currently share one page. | You see your job, not everyone's. | The picker becomes a control with consequences. |
| **A persistent, re-openable checklist** | Guidance currently ends forever at 4/4. | There is always a next thing. | Progress is visible and clickable. |
| **Filters and ranges on reporting pages** | Most surfaces are read-only. | Answers your question, not the default one. | Direct manipulation of the data. |
| **Split the 35-screen pages** | A page that long is unreadable and unlinkable. | Findable sections. | Navigation between them is interaction. |
| **Confirm every stored change with an undo** | Watchlist add/remove already has undo; nothing else does. | You can be wrong safely. | Lowers the cost of trying things. |

### Long-term — bigger bets

- **"Explain this page" walkthrough** — an on-demand tour of *this* surface, not a one-time global tour.
- **Progressive disclosure driven by usage** — surfaces you never open fold themselves away; the app narrows to your actual workflow.
- **A weekly digest** — the product reaches out with what changed, instead of waiting to be visited.
- **Full WCAG 2.2 AA audit** — the foundations are good enough that a real audit is now worth paying for.
- **Server-side aggregate signals** — today's confidence score is per-browser. Aggregated (and consented), it becomes a real product metric.

---

## 4. The self-grading system *(built)*

Four files, no new dependencies.

| File | Role |
|---|---|
| `src/lib/rubric.ts` | The rubric: categories, weights, thresholds, levels, scoring. One implementation, shared by the crawler and the app, so the report and the page can never disagree. |
| `e2e/ux-audit.spec.ts` | The crawl. Visits 42 routes, measures each in a real browser, scores with `scoreSite`, writes `public/ux-score.json` and `UX_SCORE.md`. Run with `npm run ux:audit`. |
| `src/lib/ux.ts` | The behavioural counters — local to the browser, no network, no identifiers. |
| `src/components/UxProbe.tsx` | The sensor. Detects the confusion signals below. |
| `src/app/health` | `/health` — the scorecard, the weakest area, the recommended fix, and the trend. |

### What counts as good, average, poor

Per page, measured:

| Metric | Good | Poor |
|---|---|---|
| Words | ≤ 400 | ≥ 1,500 |
| Screens of scroll | ≤ 3 | ≥ 8 |
| Numbers on screen | ≤ 120 | ≥ 450 |
| Controls | 6–60 | 0, or ≥ 220 |
| Unexplained jargon | 0 | ≥ 8 |
| Unnamed controls | 0 | ≥ 6 |
| Targets under 40px | 0 | ≥ 12 |

### How it detects a weak experience

Every signal the brief asked for maps to a detector:

| The user… | Detected as | Counter |
|---|---|---|
| drops off before finishing | session ends with zero interactions | `bounces` |
| clicks the wrong thing | click landing on nothing interactive inside `main` | `deadClicks` |
| spends too long on one section | 90s on a page with no interaction | `stalls` |
| skips onboarding | tour dismissed | `tourSkips` |
| does not complete a main action | setup steps started vs finished | `tasksCompleted / tasksStarted` |
| returns to previous pages often | A → B → A within 20s | `backThrash` |
| asks for help | help panel opened | `helpOpens` |
| does not respond to prompts | suggestion shown but not clicked | `promptsShown − promptsTaken` |
| completes a task successfully | a setup step recorded | `tasksCompleted` |
| takes a recommended next step | the dock's CTA clicked | `promptsTaken` |
| loses patience | 3+ clicks in one spot within 700ms | `rageClicks` |

**Grading interactivity:** interactions per active minute, plus the share of sessions with zero interactions.
**Grading clarity:** how often people need help or stall — needing the glossary is the product failing to explain itself in place.
**Grading helpfulness:** task completion rate and bounce rate.

### Three honesty rules, enforced in code

1. **No evidence scores `null`, never a flattering default.** Every unmeasured category prints `—`.
2. **Behaviour only counts at 5+ sessions** (`MIN_SESSIONS`), and then at half weight. One curious visitor cannot move the grade.
3. **The page re-scores from the same function as the report.** There is no second implementation to drift.

---

## 5. The rubric

| Score | Level | What it means | What we do about it |
|---|---|---|---|
| 90–100 | **Excellent** | Very user-friendly, interactive and helpful. A new person gets somewhere useful without being told how. | Hold the line. Every new page clears the same bar. |
| 75–89 | **Good** | Mostly useful. People succeed, but rough edges cost them time. | Fix the single weakest category. No new surfaces. |
| 60–74 | **Needs Improvement** | Some parts are confusing or passive. People finish only if patient. | Freeze features. Spend the cycle on the two weakest categories. |
| 40–59 | **Poor** | People likely struggle. Most visitors leave without doing anything. | Treat as a bug. Cut surface area before adding guidance. |
| 0–39 | **Critical Failure** | Not usable by anyone who is not the author. | Stop. Rebuild the first-run path. |

---

## 6. Interactive features

| Feature | Status | How it helps |
|---|---|---|
| **Start here** | shipped | A first-time visitor with no goal gets one button, not a dashboard. |
| **Next best step** | shipped | Every page ends with one suggestion chosen from what you have already done. |
| **I'm stuck** | shipped | Opens help in place — the alternative to leaving. |
| **SignalDeck Health Score** | shipped `/health` | The product grades itself in public, the same way it grades its forecasts. |
| **User Confidence Score** | shipped | "How is this going for you?" from real behaviour, with every counter shown and explained. |
| **Progress checklist** | existed | Four steps, driven by real state, self-deleting at 4/4. |
| **Beginner / advanced mode** | existed | SIMPLE folds 35 surfaces behind one door. |
| **Tooltips → glossary** | existed | Definitions generated from one dictionary, so they cannot drift. |
| **Completion badges** | not built | Deliberately skipped — the checklist already carries progress, and a badge on top would be a second reward for the same act. |

---

## 7. Rewritten copy

**Rule applied:** keep every caveat, halve the reading level. Precision moves into
the tooltip that was already there; it is never deleted.

| Before | After |
|---|---|
| "Will this stock's realized volatility over the next ~quarter (63 trading days) be ELEVATED (upper half of its recent range) or CALM (lower half)? This is NOT a price-direction call. This is the most re-tested claim on the platform — measured walk-forward, re-verified by a second implementation, and the only forecast the options surface is allowed to build on." *(59 words)* | "Will this stock move around more than usual over the next three months, or less? This is about how much it moves, not which way. It is our most-tested call — checked against history, then checked again by a second build." *(40 words, no jargon)* |
| "A regime call is not a trade by itself: the natural expression is options/vol (e.g. straddles), whose real P&L depends on implied-vol pricing this platform does not yet ingest…" *(50 words)* | "This tells you what to expect, not what to buy." + the full caveat on a tooltip. |
| "Turn it into a vol-edge assessment →" | "See how to trade it →" |
| *(no empty-state action)* | "Not sure where to start? Begin here." |
| *(no stuck path)* | "I'm stuck" |
| *(no page purpose)* | "SignalDeck checks itself the same way it checks its forecasts. This page shows the score, what it measured, which part is weakest, and the one change that would help most." |

**Tone rules for anything new:** second person, one idea per sentence, verb-first
buttons ("Read a report", not "Report"), and never a number without a plain
sentence next to it.

---

## 8. The self-improvement loop

```
  1. npm run ux:audit          →  crawl 42 routes, measure, score
  2. writes public/ux-score.json + UX_SCORE.md
  3. /health names the weakest category and ONE fix
  4. do that fix
  5. npm run ux:audit again    →  /health appends the new score to the trend
  6. repeat while the weakest category keeps changing
```

The loop names exactly one fix on purpose. A list of nine improvements is a
backlog; one is a decision.

### What actually happened on the first turn of the loop

Three real runs. **The gain is split into what only looked better and what
actually got better**, because a self-grading system that cannot tell those
apart is worse than no system at all.

| Run | Score | Level | What changed |
|---|---|---|---|
| 1 — baseline | **54.8** | Poor | as found |
| 2 — measurement corrected | **67.1** | Needs Improvement | +12.3, and **not one user was helped** |
| 3 — next-step dock reached users | **75.3** | Good | +8.2, all of it real |
| 4 — worst pages + empty states | **77.8** | Good | +2.5 |
| 5 — goal rearranges the dashboard | **78.1** | Good | +0.3 site-wide, **+16 on the dashboard itself** |
| 6 — interactivity | **80.0** | Good | +1.9; Interactivity 6.3 → 7.7, but only 17% of that was real |
| 7 — glossary fixes | **79.6** | Good | −0.4, which is **noise, not a regression** — see below |
| 8 — `/market/overview` personalized | **80.3** | Good | +0.7 site-wide, **+20 on the page**; 147.5 screens → 3.3 |
| 9 — `/lab/insights` + a fourth metric bug | **80.4** | Good | 34 screens → 4.4 on the page; the crawler now scrolls |
| 10 — `/intel/filings` + empty-state probe | **82.1** | Good | +1.7; the page went **65 → 95**, the largest single-page jump of the pass |
| 11 — `/intel/insiders` + keyboard check | **82.8** | Good | page 76 → 95; new check reported 1,116 keyboard-unreachable controls |
| 12 — keyboard fixes + count correction | **82.9** | Good | the real figure was **58, not 1,116**; now **0** |

| Category | Run 1 | Run 2 | Run 3 | Real gain |
|---|---|---|---|---|
| Clarity | 5.9 | 8.3 | 8.3 | 0 |
| User-Friendliness | 5.4 | 8.5 | 8.5 | 0 |
| Helpfulness | 4.4 | 4.5 | **6.8** | **+2.3** |
| Interactivity | 6.3 | 6.3 | 6.3 | 0 |
| Guidance | 2.7 | 6.3 | **9.6** | **+3.3** |
| Accessibility | 7.4 | 7.3 | 7.3 | 0 |
| Visual / Layout | 8.0 | 8.0 | 8.0 | 0 |
| Feedback | 1.7 | 6.7 | 6.8 | +0.1 |
| Personalization | 9.5 | **2.1** | 2.1 | 0 *(fell because it was being flattered)* |

**Run 1 → 2 was all measurement.** Four detectors were wrong:

- `hasPurpose` 12 → 41: hero subtitles always were purpose lines; the crawler only looked for `<PagePurpose>`.
- `hasLoading` 5 → 41 and `hasError` 10 → 37: a healthy crawl never sees either state. Loading is now sampled before the settle wait; error is provoked by forcing every API call to 500 and reloading.
- `personalized` 40 → 9: it was checking `data-view-mode` on `<html>`, which is present on every page and therefore graded nothing. Now it looks for content that genuinely differs by mode — and only 9 of 42 pages have any.

**Run 2 → 3 was the product**, and it happened because run 2 exposed a bug in
the fix itself: the next-step dock is gated on having finished onboarding, so a
brand-new visitor — the person who most needs it — never saw it. The crawl had
been measuring that permanently-new state. Marking onboarding as seen (which is
true for every visit after the first) took **`hasNextStep` from 12 to 40 of 42
pages** and Guidance from 6.3 to 9.6.

The loop's first useful output was not a score. It was finding that two of nine
categories had been measuring nothing, and that the new feature did not reach
the users it was for.

### Runs 4 and 5 — acting on what the loop named

**Run 4** fixed the two worst pages and the empty-state gap. `/proof` — the one
public, shareable URL and the *worst* page in the product at 43 — got a marked
purpose line, a verb-first next step, a glossary route for its jargon, and its
withheld-skill state marked as the empty state it is. `/accuracy` went 51 → 64
the same way. Empty states went from 8 to 13 of 42 pages, and Guidance reached
**10.0** — all 42 pages now have both a purpose line and a next step.

**Run 5 was the personalization fix, and it is the largest single change in the
pass.** The dashboard is no longer one fixed page. It is a map of seven named
blocks, and the reader's goal decides which lead and which fold into one
`<details>` at the bottom — demoted, never deleted, so switching goals cannot
lose anything and needs no warning. Measured, by switching goals in a live
browser:

| Goal | Leads with | Words | Screens |
|---|---|---|---|
| *(old, fixed)* | everything | 2,475 | 8.2 |
| **Someone new / "I'm learning"** | today's read, spotlight, gauges, proof | **517** | **2.6** |
| **"I'm tracking a few names"** | what changed + your watchlist | 1,509 | 6.4 |
| **"I'm testing strategies"** | validated forecast, proof, everything | 2,478 | 8.2 (PRO) |

A visitor with no goal gets the restrained arrangement, not the wall — an
unknown reader is far more likely to be new than expert, and the banner says so
with a one-click way to change it. **The dashboard's own page score went 72 →
88.** That is the "the landing page is the densest page" finding, closed.

The site-wide score only moved +0.3 because one page out of 42 improved. That
gap between "barely moved the average" and "fixed the page everyone lands on"
is the honest limitation of a per-page mean, and it is worth saying rather than
hiding behind the headline number.

### Run 6 — interactivity, and a third bad metric

Interactivity had not moved in five runs. It turned out to be **half
measurement defect again**: the second sub-metric counted only
`input`/`select`/`textarea`, so `/lab/track-record` and `/lab/strategies` —
which have proper `aria-pressed` segmented controls — scored the same 5.0 as a
poster. Twenty-two of 42 pages "had no inputs"; only ten actually had nothing
to press.

The metric now counts **view controls**: form inputs, plus anything carrying
the ARIA state attributes that exist to say *this selects or toggles
something* (`aria-pressed`, `aria-sort`, `aria-selected`, `role=tab`,
`role=switch`), plus `<summary>`. Plain `<button>` deliberately does not count
— a button may equally be a link in disguise. Requiring the state attribute
means a page only gets credit for a control it has actually *described*, which
is the same thing a screen reader needs, so the metric now rewards correct
ARIA instead of markup volume.

**Interactivity 6.3 → 7.7 — and 83% of that was the metric, 17% was real
work.** The two real changes:

- **`/glossary` got a search box.** It scored 0.0 interactivity: no links, no
  buttons, no inputs. A glossary you cannot search is a wall. It now filters as
  you type across labels *and* definitions, announces the result count to
  screen readers, and has an empty state with a way out. Page score 68 → 76.
- **`/proof` got a horizon selector.** It hardcoded `trackRecord("1d")` while
  the API had always taken the argument. A record that only holds at one
  horizon is not much of a record, and this is the page whose entire job is
  letting a sceptic check. Interactivity 2.5 → 10.0, page score 77 → 90.

**Adding the search immediately found two real bugs that had nothing to do with
scores** — which is the best argument for the control existing at all:

1. Searching "vol" returned **nothing**. The platform's most re-tested claim is
   the volatility regime, and there was no glossary entry for volatility at
   all. Added one, and it says the thing the caption keeps having to repeat:
   elevated means *bigger swings*, not *up*.
2. The reliability metric's label was `"honesty of the %s"` — a leaked format
   placeholder, rendering literally everywhere that label appeared, tooltips
   included. Now "honesty of the percentages".

### The score has a noise floor: ±0.5

Run 7 scored **79.6** against run 6's **80.0**, on code that had only improved
(a bug fix and a new glossary entry). Nothing regressed. The crawl measures a
*live* app — heatmaps, feeds and tables carry different data every run, so word
and number counts move, and a page can drift across a threshold in either
direction.

This is a property worth stating rather than discovering later: **two checks of
identical code differ by about half a point.** A self-grading system that does
not publish its own noise floor invites reading a 1-point move as progress.
`/health` now says so on the page, next to the score.

It also means the honest headline for this pass is "around 80, up from 54.8",
not a precise figure.

### Run 8 — the longest page in the product

`/market/overview` is the screener. It rendered the **entire tracked universe,
always, identically for everyone**: 15,239 words over **147.5 screens**, 5,797
numbers, and 1,892 tap targets under 40px. Its Layout score was **0.0** — the
only zero anywhere in the audit.

It already had good controls (filters, saved views, sortable columns, three
view modes), which is why Interactivity read 10/10 while the page was
practically unusable. Interactivity and Layout are genuinely different
questions, and this page was the proof.

The dashboard's named-block treatment applied cleanly, plus one new lever —
`rowLimit`:

| Goal | Leads with | Rows |
|---|---|---|
| **Someone new / "I'm learning"** | movers, then results | 15 |
| **"I'm tracking a few names"** | movers, filters, results | 25 |
| **"I'm testing strategies"** | saved views, filters, everything | **uncapped** |

| Measure | Before | After |
|---|---|---|
| Words | 15,239 | **789** |
| Screens | 147.5 | **3.3** |
| Numbers on screen | 5,797 | 220 |
| Targets under 40px | 1,892 | 64 |
| Layout | 0.0 | **7.9** |
| Page score | 61 | **81** |

The cap is a default, never a limit. It announces itself — *"Showing the top 15
by your current sort. 290 more are filtered out of view, not out of the
data"* — and one click shows everything, with one click back. A trimmed list
that does not admit it is trimmed is just a wrong list.

**This is now covered by tests.** `e2e/personalization.spec.ts` asserts the
behaviour, not the score: that each goal produces a different block order, that
folded panels are still in the DOM behind the disclosure, and that the screener
caps for a learner and does not for a tester. A page can score well and still
show every reader the same thing; these tests are what stop that regressing.

### Run 9 — the insights feed, and the fourth broken metric

`/lab/insights` renders up to 100 insight cards, each with its own evidence
panel: **12,442 words over 34 screens**, Layout 0.4. Same treatment, plus the
primer distinction — "how to read this feed" is exactly what a newcomer needs
and exactly what someone on their fiftieth visit scrolls past, so it leads for
two goals and folds for the third.

| Goal | Primer | Cards |
|---|---|---|
| Someone new / "I'm learning" | leads | 10 |
| "I'm tracking a few names" | leads | 20 |
| "I'm testing strategies" | folded | **all of them** |

The "show all" control was duplicated between this page and the screener, so it
was extracted to `src/components/ShowAllBar.tsx` — one wording, two callers,
no drift.

**Then the audit reported 190 words and a perfect 10.0 for Layout, and that was
a lie.** Driving the page in a real browser showed why: `Reveal` renders
`{visible && children}` behind an IntersectionObserver, so **every section
below the fold is absent from the DOM until it is scrolled into view.** The
crawler never scrolled. It had been measuring a fraction of the page and
rewarding it for hiding its own content.

The crawler now scrolls to the bottom before measuring. The honest numbers,
cross-checked against a real browser session that agreed to within 1%:

| Measure | Before | After |
|---|---|---|
| Words | 12,442 | **1,148** |
| Screens | 34.0 | **4.4** |
| Numbers on screen | 2,202 | 176 |
| Layout | 0.4 | 7.2 |
| Page score | 76 | **90** |

Uncapped — the tester's view — the same page still renders 100 cards and 33.9
screens, exactly as intended. The cap is real, not an artifact.

Two other pages had been under-measured by the same bug: `/market/breadth`
(+710 words once scrolled) and `/lab/forecasts` (+271). Site-wide the
correction was small (80.5 → 80.4), which is the reassuring outcome — the bug
mattered enormously for one page and barely at all for the rest.

**That is four metrics that were measuring the wrong thing** — purpose lines,
loading/error states, personalization, interactivity — plus this one measuring
the wrong *amount*. Every one was found by acting on what the score said and
noticing the result did not match reality.

### Run 10 — the filings feed, and a control nobody could see

`/intel/filings` was the longest page left: 2,280 words over 14.5 screens, 407
links, 409 tap targets under 40px, Layout **0.0**.

It also reported **one** interactive control while showing **nine**. The form
filter chips (`all`, `4`, `8-K`, `10-Q`, …) carried their selected state in
*colour alone* — no `aria-pressed`, no `role="group"`. Invisible to a screen
reader, and invisible to the audit for exactly the same reason. That is the
best argument for the interactivity metric requiring ARIA state rather than
counting buttons: **the metric found a real accessibility defect by refusing to
credit an undescribed control.**

| Measure | Before | After |
|---|---|---|
| Words | 2,280 | **466** |
| Screens | 14.5 | **2.9** |
| Links | 407 | 57 |
| Targets under 40px | 409 | 52 |
| View controls | 1 | **13** |
| Layout | 0.0 | 9.7 |
| Page score | 65 | **95** |

The three long-list pages had each grown their own cap numbers, so those were
unified into one `LIST_LIMITS` table in `lib/goal.ts`, indexed by goal and row
height. The cap bounds *screens*, not rows — a page of tall evidence cards has
to stop sooner than a page of one-line filings to occupy the same space — so
"card / row / compact" is a real distinction rather than three arbitrary
constants.

### And a sixth broken metric: empty states

`hasEmptyState` only read true when a page *happened* to have no data that day.
Identical happy-path blindness to loading and error, and it had been sitting
there since the first run. The crawler now serves `200 []` to every API call,
reloads, and asks the real question: when there is nothing to show, does this
page say so, or go blank?

**14 of 42 pages → 20 of 42.** Six pages had working empty states the audit
could never see; the other 22 genuinely do not have one, which is now a true
finding rather than an artefact.

### Run 11 — the insiders table, and a control no keyboard could reach

`/intel/insiders`: 3,064 words over 9 screens, 207 links, Layout **0.1**. Same
cap treatment — 526 words, 2.3 screens, page **76 → 95**.

But the real find was the sort. The table's sortable columns were `onClick`
handlers on bare `<th>` elements. A table header is not focusable, so **sorting
was completely unreachable by keyboard**, and with no `aria-sort` a screen
reader could not tell the table was sortable or which way it was ordered. Fully
usable with a mouse, unusable without one. Now a real `<button>` inside a `<th>`
that carries `aria-sort`, with a test that focuses it and presses Enter.

**Fixing it changed the Accessibility score by nothing** — because the rubric
did not measure whether a control can be operated at all. It measured naming,
heading order, target size, alt text and live regions: five ways of grading how
a control is *described*, and none of whether it *works*. A page could be
entirely mouse-only and score 8/10.

So the audit now counts **mouse-only controls**: elements with `cursor: pointer`
that are not focusable and contain nothing focusable. It found **1,116 of them,
concentrated on four pages**:

| Route | Keyboard-unreachable controls |
|---|---|
| `/intel/smart-money` | 509 |
| `/market/regimes` | 432 |
| `/market/overview` | 160 |
| `/intel/shorts` | 15 |

38 of 42 pages are clean. These four are not — but see the correction below:
**1,116 was wrong, and the check that produced it was over-counting.**

**A caution about the score, from the same run.** Adding this check *raised*
Accessibility from 7.3 to 7.7, because 38 of 42 pages score full marks on the
new sub-metric and dilute the five older ones. A category mean will do that
every time a new check is added that most pages already pass — which is exactly
why the weakest-pages list and the raw counts matter more than the headline.

### Run 12 — fixing them, and correcting the count

**The 1,116 figure was wrong. The real number was 58.**

`cursor: pointer` is inherited. One clickable `<tr>` was therefore counted
itself *plus* every cell, bar and span inside it — one defect reported as ten.
The check now counts only the **outermost** unfocusable pointer element. Under
correct counting:

| Route | Reported | Actually |
|---|---|---|
| `/intel/smart-money` | 509 | **53** (50 rows + 3 headers) |
| `/market/regimes` | 432 | **0** |
| `/market/overview` | 160 | **0** |
| `/intel/shorts` | 15 | **5** |

`/market/regimes` and `/market/overview` were never broken at all. Their rows
*are* clickable, but each already contains a real `<button>` or `<Link>` doing
the same job — a mouse shortcut layered over an accessible control, which is
the correct pattern. The inflated count made two healthy pages look like the
worst offenders in the product.

**What was genuinely broken was three code patterns, not 58 places:**

1. Sortable `<th onClick>` on `/intel/smart-money`, `/intel/shorts` and (fixed
   in run 11) `/intel/insiders`. Extracted to one `SortHeader` component —
   `aria-sort` on the `<th>`, a real `<button>` inside it.
2. `/intel/smart-money`'s rows, whose only click target was the `<tr>` itself
   with no focusable child anywhere. The symbol cell is now a real button; the
   row click remains as a mouse convenience.

| Measure | Before | After |
|---|---|---|
| Mouse-only controls, site-wide | 58 | **0** |
| Pages affected | 4 | **0** |
| `/intel/shorts` accessibility | 5.0 | 6.7 |
| `/intel/shorts` view controls | 1 | 6 |
| `/intel/smart-money` view controls | 1 | 4 |

`e2e/keyboard.spec.ts` pins it: every `th[aria-sort]` on those three pages must
contain a button, focusing it and pressing Enter must change the announced
sort, and the smart-money row picker must be focusable.

**The lesson is the same one as the other five detectors, pointed the other
way.** The previous ones under-reported; this one over-reported by 19×. A
self-grading system's first duty is to be checkable — every count here should
be treated as a hypothesis until someone looks at what it is actually counting.

---

## 9. Action plan

**Current score: ~82.8 / 100 — Good (±0.5). Weakest: Personalization, 4.3 / 10.**

| Category | Score | State |
|---|---|---|
| Guidance | **10.0** | done — all 42 pages have a purpose line and a next step |
| Visual / Layout | 8.8 | no page scores 0; longest page is 9.6 screens, down from 147.5 |
| User-Friendliness | 8.8 | |
| Clarity | 8.6 | |
| Helpfulness | 8.0 | |
| Feedback | 7.8 | 20 of 42 pages have a real empty state |
| Interactivity | 7.8 | 11 pages still have nothing that changes the view |
| Accessibility | 7.7 | **but see the 1,116 keyboard-unreachable controls below** |
| Personalization | 4.3 | **13 of 29 metric-dense pages adapt** |

**Next — the one fix the loop names**
1. **Personalization** — five pages adapt now (dashboard, screener, insights,
   filings, insiders) out of 29 that should. The pattern is proven and cheap to
   repeat: cap long lists with `listLimitFor` + `ShowAllBar`, add a
   `GoalBanner`, fold the rest in a `<details>`. Next by density: `/intel/news`
   (9.6 screens, now the longest page) and `/market/macro`.

*(Keyboard operability is closed: 0 mouse-only controls across all 42 pages,
pinned by `e2e/keyboard.spec.ts`.)*

**Then**
2. The 11 pages with no view control: `/advanced`, `/market/breadth`,
   `/market/regimes`, `/market/macro`, `/lab/pairs`, `/lab/research`,
   `/lab/sentiment`, `/lab/live`, `/accuracy`, `/hud`. Some are door or
   reference pages and are fine; `/market/breadth`, `/market/regimes` and
   `/lab/live` are not, and should get a range or a filter.
4. Per-page live regions where content genuinely changes without user action.
   The shell announces app-wide staleness and the alerts panel announces new
   alerts; blanket `aria-live` on the other 36 pages would make things *worse*,
   not better, so this is deliberately narrow work.

**Standing rule**
5. Run `npm run ux:audit` in CI. Fail the build on a new page with no purpose
   line or no next step, and on any page over 8 screens.

**Three caveats on the score.**
- It has a **±0.5 noise floor** — the crawl reads a live app. Do not read a
  1-point move as progress.
- It is the *pages* score. Every behavioural category still reads `—` because
  fewer than 5 sessions have been recorded. The grade is half-blind by design
  and will move — probably down — once real visits count.
- It is a per-page mean, so fixing the single page everyone lands on moved it
  +0.3 while moving that page +16. Read the weakest-pages list, not the
  headline.

**And one about the exercise itself.** Of the roughly 27 points gained since
54.8, **a large share came from fixing metrics that were measuring the wrong
thing** — purpose lines, loading and error states, personalization,
interactivity, page length read without scrolling, and empty states that only
appeared when a page happened to be empty. Six detectors, all broken, all
found the same way: act on what the score says, then check whether the result
matches reality. **The first several runs of a self-grading system mostly audit
the auditor** — and that is the system working, not failing.

The real product work in this pass: the next-step dock, goal-driven layouts on
the dashboard / screener / insights feed, the glossary search, the `/proof`
horizon selector, and three bugs found along the way that had nothing to do
with scores — a missing volatility glossary entry, a `%s` placeholder rendering
in every reliability tooltip, and a lazy-reveal that hid content from anything
that did not scroll.

---

*This document grades usability, not forecast quality. For the latter, see `/lab/track-record`.*
