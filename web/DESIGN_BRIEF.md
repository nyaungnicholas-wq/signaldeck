# SignalDeck UI v4 — "Cinematic Terminal" Design Brief

The one source of truth for the v4 redesign. Every page must follow this.
Goal: Bloomberg-terminal density fused with a cinematic HUD — a product that
looks like it costs $100k/yr. Heavy but purposeful motion.

## Stack
Next 16 (App Router), React 19, Tailwind 4, lightweight-charts v5.
NO new dependencies. Motion = CSS keyframes + IntersectionObserver + rAF.
All pages are client components fetching via `src/lib/api.ts` helpers.

## Tokens (globals.css — already exist, keep using)
- Surfaces: `--bg #060910`, `.panel` (glass), `--panel3` elevated
- Text: `--text`, `--dim`, `--faint` (never darker than --faint)
- Semantics: `--bid #34d399` (up/good), `--ask #f87171` (down/bad),
  `--accent #fbbf24` (amber brand), `--crossed #a78bfa` (purple special)
- NEW v4: `--hud #38bdf8` (cyan HUD accent — motion, glows, live elements)
- Fonts: Inter UI (`--font-inter`), JetBrains Mono data (`.tnum`/`.mono`)

## v4 motion layer (globals.css) — classes every page uses
- `.page-enter` — put on each page's root wrapper. Fade-up entrance.
- `.reveal-item` — staggered entrance for cards/rows. Set inline
  `style={{ "--i": n } as React.CSSProperties}` for stagger index n (delay n*60ms, cap ~12).
- `.hud-panel` — `.panel` upgrade: corner brackets + top scanline shimmer.
  Use for hero/marquee panels only (1-3 per page), plain `.panel` elsewhere.
- `.glow-text` (amber), `.glow-hud` (cyan), `.glow-up`, `.glow-down` — text glows for hero numbers.
- `.hero-title` — gradient shimmer page title.
- `.live-dot` — pulsing cyan dot (live/streaming indicators).
- `.bar-animate` — horizontal bars grow from 0 on entrance (transform-origin left).
- `.draw-path` — SVG paths draw themselves (stroke-dash).
- `.tick-up` / `.tick-down` — one-shot background flash for changed numbers.
Reduced-motion: globals.css already kills ALL animation globally. Never inline
`animation`/`transition` styles that would bypass it.

## v4 UI kit — `src/components/ui/Kit.tsx` (import from `@/components/ui/Kit`)
- `<Reveal>` — wraps a list/grid; children get staggered entrance when scrolled into view.
- `<AnimatedNumber value={n} decimals={d} prefix suffix />` — rAF count-up, .tnum mono.
- `<Spark data={number[]} width height color fill />` — animated sparkline SVG.
- `<Gauge value min max label color />` — radial arc gauge, animated sweep.
- `<StatTile label value sub delta spark />` — hero stat tile.
- `<PageHero title subtitle right>` — standard page header: shimmer title,
  subtitle in --dim, optional right-side slot (filters/live-dot).
- `<DeltaBadge value />` — signed % chip, green/red, arrow triangle.
- `<MiniBar value max color />` — animated inline bar for table cells.

## Page anatomy (every redesigned page)
1. Root: `<div className="page-enter space-y-4">`
2. `<PageHero>` at top — real title + one-line plain-English subtitle saying
   what this page tells you (the old pages often lack this).
3. A hero band: 3-5 `<StatTile>`s in a responsive grid (`grid gap-3
   sm:grid-cols-2 xl:grid-cols-4`) surfacing the page's headline numbers with
   AnimatedNumber + sparklines.
4. Content panels below — `.panel` with `.panel-h` headers; marquee panel may
   use `.hud-panel`. Tables stay in `.table-wrap`.
5. Wrap card grids/table sections in `<Reveal>` with `.reveal-item` children.

## Rules
- Density with hierarchy: big mono numbers (text-2xl/3xl .tnum) for heroes,
  0.75rem floors for captions. NEVER below text-[0.75rem].
- Color = meaning: green up, red down, amber brand/warn, cyan live/HUD,
  purple special. Don't decorate with semantic colors.
- Numbers always `.tnum`. Tickers `.mono`.
- Every chart/bar animates on entry (draw-path / bar-animate).
- Hovers: brighten border/bg only — NO scale transforms (layout shift).
- cursor-pointer on all clickable; keep existing keyboard focus behavior.
- Empty/loading: use existing `Skeleton` component or `.skeleton-bar`.
- SVG icons only (inline paths, 24x24 viewBox) — never emoji.
- Keep ALL existing data fetching, API calls, types, and business logic
  intact unless the task says otherwise. This is a re-skin + UX upgrade,
  not a data rewrite.
- Preserve existing exports/props so sibling imports don't break.
