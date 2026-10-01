# Stable public URL for SignalDeck - runbook (2026-10-01)

Today the public site is the free Cloudflare quick tunnel run by the scheduled task 'SignalDeck Quick Tunnel' (`cloudflared tunnel --url http://127.0.0.1:8323`, log `logs\quicktunnel.log`). The daemon reads the newest trycloudflare URL from that log through `SIGNALDECK_TUNNEL_LOG`. Nothing in this file has been done yet; every step marked OWNER needs the owner's own account, card, login or an elevated shell.

## 1 Why the quick tunnel is not enough

- Between 2026-09-27 22:55 PT and 2026-10-01 the task issued 4 different trycloudflare URLs in about 3.3 days. Lifetimes were 31.7 h, 5.9 h and 4.5 h. Causes: the host was powered off twice, one console-exit (0xC000013A) that RestartOnFailure did not restart, and the first registration.
- Every rotation breaks: emailed verify links (valid 24 h) and reset links (valid 1 h), the host-only session cookie (every member is signed out), bookmarks, Google sign-in origins, Turnstile hostnames, `NEXT_PUBLIC_SITE_URL` (sitemap, Open Graph) and the `--site` line in `ops/anchors-VERIFY.md`.
- Cloudflare documents quick tunnels as for testing and development only, with a cap of 200 in-flight requests (more get 429), no Server-Sent Events (SSE) and no uptime guarantee.
- SSE is operator-only here: the only SSE consumer is `web/src/hooks/useSnapStream.ts` (reads `/api/stream/snaps`), mounted only on the operator symbol page. Members are unaffected.

## 2 Options, ranked

A) Recommended - a domain from Cloudflare Registrar plus a named tunnel (the documented production path).
- A .com costs about USD 10.46/yr now (registry fee 10.26 + ICANN fee 0.20, sold at cost) and about USD 11.17 after 2026-11-01, when the registry fee rises to 10.97. These totals are our arithmetic, not a published Cloudflare price: UNCONFIRMED until the dashboard shows them. Buy before 2026-11-01.
- Registrar domains always use Cloudflare nameservers, so the domain is a usable tunnel zone straight away.
- Public hostname e.g. `app.<domain>` -> `http://127.0.0.1:8323`.
- OWNER: create and verify a Cloudflare account; add a payment method (accepted methods UNCONFIRMED); buy the domain; in the dashboard create a named tunnel and run the shown `cloudflared service install <token>` yourself (the token is a credential; the service starts at boot, no logon needed); add the public hostname `app.<domain>` -> `http://127.0.0.1:8323`. Then do section 3.

B) $0 - a workers.dev Worker with a Workers VPC binding to a named tunnel. No domain needed.
- Workers VPC is beta and free on all Workers plans during the beta; pricing after beta UNCONFIRMED.
- Workers Free allows 100,000 requests a day, and every Next.js asset counts. Needs cloudflared 2025.7.0 or later and outbound QUIC.
- Unverified: whether SSE works through the binding, and which client IP the daemon's rate limiter sees behind a Worker. With `SIGNALDECK_TRUST_PROXY` the limiter keys on the last X-Forwarded-For hop; behind a Worker that hop may be the Worker's egress IP (every login and sign-up in one bucket) or client-supplied (spoofable). Test both before relying on it.
- OWNER: create the Cloudflare account; claim a workers.dev subdomain; create the tunnel under Workers -> VPC -> Tunnels and run its `cloudflared service install <token>`; approve `npx wrangler login` in your browser. After that the Worker, `npx wrangler vpc service create signaldeck-web --type http --tunnel-id <id> --ipv4 127.0.0.1 --http-port 8323` and `npx wrangler deploy` can be scripted.

C) Tailscale Funnel - a free stable `<machine>.<tailnet>.ts.net` name.
- Beta, non-configurable bandwidth limits, and the free Personal plan is worded "only suitable for non-commercial use". Windows support for Funnel UNCONFIRMED.
- OWNER: install Tailscale and log in; enable MagicDNS, HTTPS certificates and the funnel attribute in the admin console; run `tailscale funnel --bg 8323`.

Ruled out (one bullet each):
- ngrok free, including the reserved `spearfish-dwindle-module.ngrok-free.dev`: an interstitial page in front of all browser HTML, and 1 GB plus 20,000 requests a month. One forgotten tab's health poll alone used to send 8,640 requests a day.
- A GitHub Pages redirect: up to 10 minutes to publish plus a 10 minute CDN cache, and it still lands visitors on the rotating URL, so bookmarks and sessions still break.
- An Oracle Always Free host is optional and a separate question: 2 OCPU / 12 GB / 200 GB arm64, idle instances get reclaimed, capacity errors are common. It does not fix the hostname by itself.

## 3 Cutover - in this exact order

1. In `daemon/.env` FIRST: set `SIGNALDECK_PUBLIC_URL=https://<host>` and add `https://<host>` to `SIGNALDECK_WEB_ORIGINS`, keeping the localhost entries: `SIGNALDECK_WEB_ORIGINS=https://<host>,http://localhost:8323,http://127.0.0.1:8323,http://localhost:3000,http://127.0.0.1:3000`. `SIGNALDECK_ALLOWED_HOSTS` needs no new entry: the web proxy calls the daemon as `127.0.0.1:8322`.
2. Restart the daemon (it reads `daemon/.env` only at start): `bash ops/signaldeck-ctl.sh deploy`.
3. Confirm `published()` is still true. Sign in at `https://<host>` with a NON-admin account and open `https://<host>/api/auth/me`: it must show `"member":true` (for a non-admin account that flag is exactly `published()`). Also, signed out, from Git Bash: `curl -s -o /dev/null -w "%{http_code}" "https://<host>/api/export/bars.csv?symbol=AAPL"` must not print 200 (401 or 451 is right). If either check fails, stop and put the old `.env` back.
4. Only then, optionally: remove `SIGNALDECK_TUNNEL_LOG`, redeploy, and repeat step 3. (The ngrok host in `SIGNALDECK_ALLOWED_HOSTS` belongs to the weekday webhook tunnel 'SignalDeck Tunnel'; drop it only when that tunnel is retired, and only after this step.) Then retire the quick tunnel in an elevated shell: `Disable-ScheduledTask -TaskName 'SignalDeck Quick Tunnel'` (web-guard leaves a disabled task alone). Note: once `SIGNALDECK_PUBLIC_URL` is set, the old trycloudflare origin is no longer allowed, so sign-in from the old URL gets 403.
5. Rebuild the web app with the site URL (inlined at build time, not read at runtime), in PowerShell from the repo root:
   `$env:NEXT_PUBLIC_SITE_URL = 'https://<host>'; powershell -NoProfile -File ops\web-release.ps1 -Port 8323 -Task 'SignalDeck Web'`
   `NEXT_PUBLIC_SIGNALDECK_PUBLIC` is a separate decision (where anonymous visitors land); the cutover does not need it.
6. When they exist: add `https://<host>` to the Google OAuth client's Authorized JavaScript origins and to the Turnstile widget's hostnames.
7. In `ops/anchors-VERIFY.md` replace the placeholder with `python verify.py . --site https://<host>`.

### Why the order matters

The public web tier is the keyed local proxy. 'SignalDeck Web' runs `ops/start-local-workspace.ps1 -Port 8323`, which sets `SIGNALDECK_LOCAL_ONLY_PROXY=1` and loads `SIGNALDECK_LOCAL_PROXY_KEY` from `daemon/.env`, so the Next proxy (`web/src/app/api/[...path]/route.ts`) stamps `x-signaldeck-local` on every request it forwards. Every tunnel visitor therefore reaches the daemon as a keyed loopback request. If `published()` goes false, every visitor is treated as local: licensed raw bars and CSV exports are served (`rawDataRefused`, `daemon/internal/api/api.go`) and every signed-in member gets operator authority (`isOperator`, `daemon/internal/api/accounts.go`). `published()` is true when any of `SIGNALDECK_PUBLIC_SURFACE`, `SIGNALDECK_PUBLIC_URL`, `SIGNALDECK_TUNNEL_LOG` or a non-loopback host in `SIGNALDECK_ALLOWED_HOSTS` is present; today it rests on `SIGNALDECK_TUNNEL_LOG` plus the ngrok host. Removing those before `SIGNALDECK_PUBLIC_URL` is live is the one mistake that leaks.

## 4 Until the cutover (quick tunnel hardening)

- `ops/web-guard.ps1` (every 5 minutes, 24/7) starts 'SignalDeck Quick Tunnel' when no quick-tunnel cloudflared is running, and leaves a disabled task alone. A restart still rotates the URL.
- The task definition in `ops/tasks/SignalDeck Quick Tunnel.xml` now has a BootTrigger, so the site comes back after an unattended reboot without anyone logging on. OWNER: once this change is in the live checkout, re-register it in an elevated Windows PowerShell (as Nicholas_N) with this block. Updating a task does not stop its running instance (the 2026-09-30 23:25 update left cloudflared running), so the URL does not rotate:
```powershell
$repo = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
. "$repo\ops\lib-tasks.ps1"
$xml = Expand-TaskTokens -Xml (Get-Content -Raw -LiteralPath "$repo\ops\tasks\SignalDeck Quick Tunnel.xml") -Repo $repo
Register-ScheduledTask -TaskName 'SignalDeck Quick Tunnel' -Xml $xml -Force | Out-Null
(Get-ScheduledTask -TaskName 'SignalDeck Quick Tunnel').Triggers | ForEach-Object { $_.CimClass.CimClassName }
```
  It must print `MSFT_TaskLogonTrigger` and `MSFT_TaskBootTrigger`.
- Known limit: the daemon looks for the newest URL only in the last 256 KB of `logs\quicktunnel.log`. After roughly 8 days of unbroken uptime the banner falls out of that window, and sign-up mail answers 503 and every browser POST from the tunnel origin gets 403 until the tunnel restarts. A stable `SIGNALDECK_PUBLIC_URL` removes the problem.

## 5 Superseded advice (removed from the 2026-09-20 version)

- It said to set `SIGNALDECK_OPEN_SIGNUP=false`. Do not: that closes member sign-ups, which are now the product.
- Its staged task 'SignalDeck Cloudflare Tunnel' (`ops/tasks`, listed in `ops/tasks/.pending`) starts only at logon, so after an unattended reboot nothing starts it. Prefer the `cloudflared service install <token>` service; if you use the task instead, give it a BootTrigger first.
- Do not register task XML with `ops/install-windows-tasks.ps1 -Install` for this: it registers the stored `{{USER}}`/`{{REPO}}` placeholders without expanding them. Use the `Expand-TaskTokens` block above.