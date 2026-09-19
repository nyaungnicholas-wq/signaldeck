# Restoring the SignalDeck public tunnel (O3) — prepared 2026-08-11

**Status: BLOCKED on a decision, not on a command.**

The `SignalDeck Tunnel` scheduled task does not exist, and no version of a
registration command works today. Verified on this machine:

| Fact | How it was checked |
|---|---|
| Task not registered | `Get-ScheduledTask -TaskName 'SignalDeck Tunnel'` → not found |
| `install-windows-tasks.ps1` skips it **by design** | it requires a `.sh` in `ProgramArguments`; `com.signaldeck.tunnel.plist` names the ngrok binary directly |
| **ngrok is NOT installed** | `command -v ngrok` → missing. The plist points at `/Users/natalienyaung/.local/bin/ngrok` (a macOS path) |
| **cloudflared IS installed, but has no tunnel** | `C:\Program Files (x86)\cloudflared\cloudflared.exe` exists; `%USERPROFILE%\.cloudflared\` does not |
| The daemon would reject the webhook regardless | `AllowedHosts` defaults to `127.0.0.1:8322,localhost:8322`; the public host must be in `SIGNALDECK_ALLOWED_HOSTS` |

Registering a tunnel republishes this daemon to the internet. That is an
outward-facing change, so it needs an explicit go-ahead — not an inferred one.

## What it is for

TradingView alert webhooks → `POST /api/tv-webhook` on the daemon (`:8322`).
Nothing else depends on it. **If you are not currently using TradingView
webhooks, the correct action is to do nothing** and let the task stay absent —
that is the cheapest and safest outcome, and it closes O3 as "not wanted".

---

## Option A — reinstate ngrok (matches the existing plist and reserved domain)

Keeps `spearfish-dwindle-module.ngrok-free.dev`, the reserved free static domain
already named in the plist, so nothing else has to change.

Prerequisites you must do yourself (both are account actions):

```bash
winget install ngrok.ngrok
```

```bash
ngrok config add-authtoken <YOUR_NGROK_AUTHTOKEN>
```

Then, in an **elevated** PowerShell (Run as Administrator), from the repo root:

```powershell
$ng = (Get-Command ngrok).Source; $a = New-ScheduledTaskAction -Execute $ng -Argument 'http 8322 --domain=spearfish-dwindle-module.ngrok-free.dev --log=stdout'; $p = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType S4U -RunLevel Limited; $s = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries; Register-ScheduledTask -TaskName 'SignalDeck Tunnel' -Action $a -Principal $p -Settings $s -Force
```

No trigger, deliberately: the plist model is on-demand
(`RunAtLoad=false`, no `KeepAlive`) — `signaldeck-ctl.sh up|collect` kicks it,
market-close stops it. That matches how `SignalDeck Web` is registered.

`-ExecutionTimeLimit ([TimeSpan]::Zero)` = no cap, because this is a long-lived
service. `install-windows-tasks.ps1` applying a 6h cap to services was a prior
incident; do not add one here.

## Option B — switch to cloudflared (already installed, no new software)

Requires a browser login and a named tunnel, both of which you must run:

```bash
cloudflared tunnel login
```

```bash
cloudflared tunnel create signaldeck
```

```bash
cloudflared tunnel route dns signaldeck <YOUR_HOSTNAME>
```

Then, **elevated**, from the repo root:

```powershell
$cf = 'C:\Program Files (x86)\cloudflared\cloudflared.exe'; $a = New-ScheduledTaskAction -Execute $cf -Argument 'tunnel run --url http://127.0.0.1:8322 signaldeck'; $p = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType S4U -RunLevel Limited; $s = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries; Register-ScheduledTask -TaskName 'SignalDeck Tunnel' -Action $a -Principal $p -Settings $s -Force
```

Option B also requires updating `com.signaldeck.tunnel.plist` and the TradingView
alert URLs to the new hostname. Option A avoids both.

---

## Required for EITHER option: the Host allowlist

The daemon drops requests whose `Host` header is not allowlisted. Default is
`127.0.0.1:8322,localhost:8322`, so a tunnel's traffic is rejected until the
public hostname is added. Set it wherever the daemon's environment is defined
and restart the daemon:

```
SIGNALDECK_ALLOWED_HOSTS=127.0.0.1:8322,localhost:8322,spearfish-dwindle-module.ngrok-free.dev
```

## Verification after whichever path you take

Run all three — a registered task proves only that a task is registered:

```powershell
Get-ScheduledTask -TaskName 'SignalDeck Tunnel' | Select-Object TaskName,State
```

```bash
bash ops/signaldeck-ctl.sh status
```

```bash
curl -sS -o /dev/null -w "%{http_code}\n" https://spearfish-dwindle-module.ngrok-free.dev/api/health
```

The third is the one that matters: `200` means the tunnel is up AND the Host
allowlist accepts it. A `404`/`421`/connection error means the tunnel is running
but the daemon is refusing the Host — fix `SIGNALDECK_ALLOWED_HOSTS`, not the
tunnel.

## Resolved since this runbook was written (2026-09-19)

The complaint that `ops/lib-portable.sh` discarded `schtasks //Run`'s exit status is
**stale**. `sd_svc_start` (`lib-portable.sh:277-292`) now probes with `//Query` and
returns 2 = not registered, 1 = refused, 0 = started, and `kick()`
(`signaldeck-ctl.sh:61-63`) inspects and reports it. A tunnel that fails to start is
visible.

Option A's hand-typed `Register-ScheduledTask` one-liner is also superseded. The
tunnel is now a managed definition at `ops/tasks/SignalDeck Tunnel.xml`, so once
ngrok is installed and the authtoken is set, standing it up is:

```powershell
.\ops\install-windows-tasks.ps1 -Install
```

run from an ELEVATED PowerShell (registering an S4U principal returns
"Access is denied" otherwise). Everything above about the authtoken, the reserved
domain and the Host allowlist still applies.
