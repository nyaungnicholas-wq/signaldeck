# SignalDeck Landing Page — Superprompt
<!-- SUPERSEDED-SNAPSHOT -->
> ## 📛 SUPERSEDED LIVE RECORD — HISTORICAL
> **Marked 2026-08-04 by P2.** Any live accuracy, baseline or sample size quoted
> below is the record **as it stood when this document was written**, not the
> current one. It is kept because a dated record is evidence; it is labelled
> because four such records were once in circulation with nothing to tell them
> apart (FC1).
>
> **The one authoritative live record is `partials/live_accuracy.md`**, generated
> from `data/accuracy_registry.json` by `tools/live_accuracy.py`. **If you are following this document as a brief, take every accuracy figure from that partial and include it rather than restating it — do not copy the numbers below into anything you produce.** Reconciliation:
> `proofs/P2_LIVE_RECORD_RECONCILIATION.md`.


Build a high-tech, 3D, scroll-driven case-study page for SignalDeck that makes a quant recruiter stop scrolling.

**Where it lives:** `~/claude code/nyaung-studio/work-signaldeck.html`, following the existing `work-*.html` pattern. Add the entry to `data.js`, then regenerate with `node gen-work.mjs && node gen-cases.mjs` (work.html's grid and ItemList JSON-LD are generated — never hand-edit them).

---

## THE FRAMING RULE — read before writing a single line

This is a **technical case study**, not a product marketing page. It never sells predictions, offers signals, or invites anyone to trade. That is a hard constraint with three independent reasons behind it:

1. **Compliance.** A public, money-adjacent page implying tradeable market predictions reads as compliance-unaware to exactly the firms being targeted. The LLM council explicitly rejected public deployment on these grounds.
2. **Honesty.** SignalDeck's live directional record is **46.7% over 8,191 independent symbol-days, with −25.2% Brier skill and IC −0.02** — proven *negative* skill. Any page implying predictive power would be false.
3. **Precedent.** This site has already had to correct inflated numbers once (a claimed 20.7% CAGR against a real 19.0% on *paper* trading). Do not regress. Every number on this page must trace to a verifiable source in the repo.

**The honest failure IS the asset.** Instrumented, self-caught failure is rare and it is the single most impressive thing here. Lead with it. The narrative spine is *"How I built a trading signal, proved it didn't work, and built the machinery that proved it."*

**Never on this page:** projected returns, hypothetical P&L, "start trading", email capture for signals, testimonials, fake logos, countdown timers, or any accuracy number shown without its conviction band and caveat.

---

## Design system

**Extend the existing site — do not invent a parallel one.** Reuse `styles.css` tokens verbatim:

```
--bg #060609   --panel #10101A   --text #F5F5FA   --muted #A9A9C6
--indigo #6366F1  --violet #818CF8  --fuchsia #C084FC  --cyan #38BDF8  --emerald #34D399
--line rgba(255,255,255,0.07)   --radius 16px   --ease cubic-bezier(0.22,1,0.36,1)
```

Fonts stay on the existing `--font-display` / `--font-body` / `--font-mono` variables. Dark-mode OLED only — no light mode on this page.

**Stack (already in the repo — reuse, don't add dependencies):** Three.js (ESM), GSAP + ScrollTrigger, Lenis smooth scroll, and the existing persistent `#hero-canvas` pattern in `scene.js`.

**Accent semantics — use color to mean something.** Cyan/indigo = validated structural findings. Fuchsia = the killed or retired signal. Emerald = process integrity (pre-registration, hash chain, ledger). Amber/red is reserved for the honest-failure beats. Never decorative-only.

---

## The scroll narrative — 7 acts

Each act pairs a 3D behavior with a **real** finding. The motion must visualize actual data, not abstract particles. Decorative WebGL on a quant page reads as noise; data-driven WebGL reads as capability.

**Act 1 — The wall.** Hero. A dense 3D point field of daily returns. Camera pushes in; the field resolves into pure noise. Headline states the thesis plainly: most of this is unpredictable, and here is the proof. Sub-line names the honest live record. No CTA button — a single scroll cue.

**Act 2 — The arithmetic ceiling.** Scroll-scrub a 3D surface of Sheppard's arcsin relation, `P(correct) = ½ + arcsin(IC)/π`. A marker walks from the world-class band (IC 0.10–0.17 → 53–55%) to where 70% would demand IC 0.5878. The gap is the point: it is arithmetic, not effort. Annotate that the realistic system IC of ~0.05 lands at 51.6%.

**Act 3 — The kill.** The flagship directional model. Show the live prequential record accruing as a 3D ribbon settling below the 50% plane: **46.7%, 8,191 independent symbol-days, Brier skill −25.2%**. Then the auto-retirement fires. Fuchsia. This is the emotional center of the page — give it room and do not soften it.

**Act 4 — What survived.** Pivot to what *is* predictable: structure, not direction. Interactive 3D conviction bands for trend21 (**83.3% all decisions → 97.2% at conviction ≥0.9, CI [0.965, 0.978]**) and vol63 (**76.0% top band, CI [0.696, 0.789]**). Let the user drag a conviction slider and watch accuracy climb. Every band displays its own accuracy — never the population average.

**Act 5 — The trap.** The most sophisticated beat on the page. Overlay forward return on the same conviction axis: as accuracy rises to 97.2%, mean 21-day forward return goes **negative (−0.39%; trend63's top band −1.30%)**. Two lines diverging in 3D. The lesson stated plainly: *accuracy is a persistence statistic, never an expected return.* Anyone who reads only this act should understand why "high accuracy" ≠ "makes money."

**Act 6 — The machinery.** How the mistakes get caught. A 3D hash-chain of the **203k-row prediction ledger**; the **pre-registration** of six predictors frozen twelve days before their first grade; matched nulls that killed the 52-week-high magnet (82% raw accuracy, but *negative* skill against a matched null); the gap-fill signal **pulled from production** after a day-0 conditioning bug was found. Show rejections with equal weight to wins — a search that records only winners can't be audited.

**Act 7 — Close.** Quiet. Stack, scale (163,216 weekly observations), what it runs on, and a link to the postmortem. One restrained CTA: read the write-up or view the repo. No signup.

---

## Motion rules

- Scroll-scrub with ScrollTrigger; pin each act, scrub the 3D state to progress. Lenis handles smoothing.
- One idea per act. Never animate two concepts at once.
- Camera moves are slow and deliberate — this is an instrument, not a game trailer.
- Micro-interactions 150–300ms; transform/opacity only, never width/height/top/left.
- Every number counts up **to its real value** and holds; never overshoots for drama.

## Performance and accessibility — non-negotiable gates

- **Mobile skips WebGL entirely**, matching `scene.js`'s existing `max-width: 768px` guard. A static CSS/SVG fallback carries the narrative. Mobile is 60%+ of traffic and Core Web Vitals matter more than the effect.
- **`prefers-reduced-motion: reduce`** → all scrub animation off, content shown in final state, page fully readable top to bottom.
- Progressive enhancement: if WebGL init throws, the CSS ambient background carries it (same `.catch()` pattern as `scene.js`).
- Lazy-init each act's 3D via IntersectionObserver; dispose geometries/materials on exit. One renderer, not seven.
- Targets: LCP < 2.5s, CLS < 0.1, no long task > 200ms during scroll, 60fps on an M-series laptop.
- Keyboard navigable, visible focus rings, 4.5:1 contrast minimum, alt text and `aria-label` on every canvas and icon-only control.
- SVG icons only (Heroicons/Lucide) — never emoji.
- Responsive at 375 / 768 / 1024 / 1440.

## Data sourcing

Pull every figure from the repo — `PREDICTION_PROCESS.md`, `PREREGISTRATION.md`, `audits/`, and `tools/accuracy_registry.py --json`. **Hardcode them as a single `SIGNALDECK_FACTS` constant at the top of the page script, each with a source comment.** No live API calls: the daemon is local-only and the page must be a static artifact. If a number can't be traced to a source, cut the claim rather than approximating it.

## Verification before you call it done

1. `preview_start` the `nyaung-studio` launch config (port 5190) and open `work-signaldeck.html`.
2. `read_console_messages` — zero errors, including WebGL context warnings.
3. Scroll-drive all 7 acts; screenshot each pinned state and confirm the intended 3D state actually renders.
4. `resize_window` to 375px — confirm WebGL is skipped and the fallback narrative is complete and readable.
5. Re-run with reduced-motion emulated; confirm full readability with no scrub.
6. Verify every displayed number against its repo source one final time.
7. Report with screenshots. Do not claim done without them.

## Deliverables

- `work-signaldeck.html` + any page-scoped JS/CSS, matching existing file conventions.
- `data.js` entry, then `node gen-work.mjs && node gen-cases.mjs`.
- Do **not** `git commit` and do **not** deploy. Leave the diff for review — publishing SignalDeck is a separate decision that has been deliberately deferred.
