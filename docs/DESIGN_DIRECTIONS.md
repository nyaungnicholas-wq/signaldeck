## Why a pass and not a redesign

The terminal system shipped in 2026-07 already passes the audits that are expensive to redo: dark OLED palette via CSS variables, contrast raised to WCAG AA, Inter for UI text and JetBrains Mono for numbers with tabular figures, and a 12px type floor that holds at both 1440 px and 390 px. The 54 full-page screenshots taken on 2026-09-08 showed the problems were hierarchy and copy, not palette, type, or colour. A redesign would discard working, contrast-audited tokens and the charts' dark ground for a look the evidence did not ask for, so the right move was a calm-instrument pass on top of what already works.

## Direction A — calm instrument

Keep the terminal system and tighten it. Fewer header chips, one onboarding surface instead of three, and refusal or withheld states summarised with the full grader text behind a `<details>` element. Public pages get consistent chrome, copy names the statistical unit (deduplicated symbol-day observations, distinct days), and tables render in mono with tabular figures so columns line up.

## Direction B — light editorial

A white ground, serif headlines, generous whitespace, and narrative pages aimed at first-time visitors. It would have meant rebuilding the palette, redoing the charts for a light ground, and rewriting the copy register across every route. The screenshots did not show visitors asking for that change, and it would have discarded the contrast work already done.

## Direction C — dense desk

A multi-column grid, smaller type, and more panels per screen aimed at practitioners. The audit flagged congestion, not sparsity, and pushing type below the 12px floor would break the rule the system was built around. More panels per screen would worsen the very problem the pass is meant to fix.

## Chosen direction and why

Direction A. The token system, dark theme, and mono-number conventions already hold up on both widths and pass the 12px floor, so the measured problems were hierarchy and copy, not palette. B would discard working contrast-audited tokens and the charts' dark ground for a look the evidence did not ask for. C would worsen the congestion the audit flagged and push type below the floor.

## What shipped on 2026-09-08

RefusalNotice, a summary headline plus a `<details>` element with `data-status` and `data-tone` stamps, used on the landing page, `/accuracy`, `/proof`, and the dashboard proof strip. PublicNav in the public header with Grades, Risk estimates, Receipts, Glossary, and Sign in. Page titles fixed. The landing "what is still true" list now sits under the refusal. `/proof` and the dashboard copy changed from "independent" to "deduplicated symbol-day observations over N distinct days". `/accuracy` rows lead with the daemon's publication status and quote the grader sentence labelled.

## Not done yet

Done later on 2026-09-08 (ledger F9, F15): one onboarding surface at a time on the dashboard (goal question first, checklist after a goal is chosen, no dock there); the freshness, view, reading and daemon controls sit behind one "status" disclosure with a liveness dot; the experimental directional section on the symbol page is collapsible; the symbol, screener and flagship paper endpoints are body-cached and warmed.

Still open:
- Collapsing more of the symbol page's secondary panels by default in the PRO view.
- Sign-in still waits behind long worker write transactions right after a daemon restart.

## Rules to keep

- Numbers in mono with tabular figures.
- Never below 12px.
- One purpose line per page.
- A refusal never dumps raw grader text.
- Tables scroll inside their panel.
- Colours only through the CSS variables.
- Every withheld state names its reason and its next step.