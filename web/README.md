# SignalDeck web

Next.js 16 App Router front end. It renders what the daemon says; it computes
nothing itself and holds no market logic.

## Running it

    npm run dev      # :8323, expects signaldeckd on :8322
    npm run build
    npm run start
    npm run lint
    npm run e2e      # Playwright; needs a LIVE daemon with a real database

## The one architectural rule

The browser never talks to the daemon directly. Every request goes to this
Next server and out through `src/app/api/[...path]/route.ts`, which proxies to
`SIGNALDECK_DAEMON` over loopback. That proxy deliberately does NOT forward
`SIGNALDECK_API_TOKEN`: the token maps to the admin user, so attaching it would
hand admin identity to every anonymous visitor and silently defeat
`SIGNALDECK_PUBLIC_READS=false`. The browser's only credential is its session
cookie.

## Public versus private routes

`src/lib/publicRoutes.ts` is the single list of routes an anonymous visitor may
see. It is the BROWSER's half of that decision and it is not the authority:
`daemon/internal/api/security.go`'s `publicRoutes` allowlist answers
independently on every request. Two gates that must both be wrong before
anything leaks is the arrangement worth having, because a routing mistake here
cannot expose data the daemon still refuses to serve.

Keep the two lists in step when adding a public page.

`/` is the PUBLIC landing page. The authenticated dashboard lives at
`/dashboard`; anything linking an operator "home" wants that one.

## Numbers on the page

No component may hardcode an accuracy figure. Everything comes from
`data/accuracy_registry.json` or the daemon at request time, and when the
source is unreadable the surface says so and shows nothing rather than falling
back to a stale constant. Three different accuracy figures for one predictor
were once in simultaneous circulation because a page had typed one in;
`src/app/page.tsx` and `src/app/accuracy/page.tsx` both carry the rule in their
headers.

## Design system

Tailwind 4, no config file, no component library. Tokens live on `:root` in
`src/app/globals.css`; shared primitives are in `src/components/ui/Kit.tsx`.
Numbers carry the `tnum` class so columns line up. Dark only, deliberately.
