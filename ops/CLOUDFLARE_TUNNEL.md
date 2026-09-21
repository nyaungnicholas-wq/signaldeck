# Publishing SignalDeck through a Cloudflare Tunnel ($0) — prepared 2026-09-20

**BLOCKED on one human step: `cloudflared tunnel login` (browser authorisation by the account owner). Everything else is prepared below.**

---

## 1 Prerequisites

- Windows 11 with cloudflared 2026.7.3 at `C:\Program Files (x86)\cloudflared\cloudflared.exe`
- Cloudflare account with a domain fully managed on Cloudflare (free account works)
- SignalDeck repo cloned locally; Next.js web app listens on `http://127.0.0.1:8323`
- Go daemon listens on `http://127.0.0.1:8322` — **never expose this port**
- `%USERPROFILE%\.cloudflared\` does not exist yet
- Elevated PowerShell available for task installation

---

## 2 Create the tunnel

**Human step (cannot be automated):**

```powershell
cloudflared tunnel login
```

A browser opens; authorise with the Cloudflare account that owns the domain.

Then (can be scripted):

```powershell
cloudflared tunnel create signaldeck
cloudflared tunnel route dns signaldeck <hostname>
```

Replace `<hostname>` with your domain (e.g., `signaldeck.example.com`).

---

## 3 config.yml

Create `%USERPROFILE%\.cloudflared\config.yml` with the following content:

```yaml
tunnel: <tunnel-id>
credentials-file: %USERPROFILE%\.cloudflared\<tunnel-id>.json
ingress:
  - hostname: <hostname>
    service: http://127.0.0.1:8323
  - service: http_status:404
```

- `<tunnel-id>` is the UUID printed by `cloudflared tunnel create signaldeck`
- `<hostname>` is the same domain used in `route dns`

---

## 4 The scheduled task

Save as `ops/tasks/SignalDeck Cloudflare Tunnel.xml`:

```xml
<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Runs cloudflared tunnel for SignalDeck web app on port 8323</Description>
    <URI>\SignalDeck Cloudflare Tunnel</URI>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <UserId>{{USER}}</UserId>
      <LogonType>S4U</LogonType>
    </Principal>
  </Principals>
  <Settings>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <UseUnifiedSchedulingEngine>true</UseUnifiedSchedulingEngine>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>{{USER}}</UserId>
    </LogonTrigger>
  </Triggers>
  <Actions Context="Author">
    <Exec>
      <Command>C:\Program Files (x86)\cloudflared\cloudflared.exe</Command>
      <Arguments>tunnel --logfile "{{REPO}}\logs\cloudflared.log" --loglevel warn run signaldeck</Arguments>
      <WorkingDirectory>{{REPO}}</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
```

Install with elevated PowerShell:

```powershell
powershell -File ops\install-windows-tasks.ps1 -Install
```

The task carries its own restart policy (1 minute x 999) in `<Settings>`; the installer adds none. Logs go to `{{REPO}}\logs\cloudflared.log` (the logs directory already exists).

---

## 5 Daemon env changes

Edit `daemon/.env` and set **exactly** these values (one line each):

```
SIGNALDECK_ASSUME_TUNNEL=1
SIGNALDECK_ALLOWED_HOSTS=<hostname>,127.0.0.1:8322,localhost:8322
SIGNALDECK_WEB_ORIGINS=https://<hostname>
SIGNALDECK_PUBLIC_SURFACE=1
SIGNALDECK_PUBLIC_READS=false
SIGNALDECK_OPEN_SIGNUP=false
```

| Variable | Purpose |
|----------|---------|
| `SIGNALDECK_ASSUME_TUNNEL=1` | Prevents `reachablePrivately()` from treating loopback as private (would open signup, anonymous reads, disable 451 guard) |
| `SIGNALDECK_ALLOWED_HOSTS` | Host allowlist; loopback pair mandatory because web proxy doesn't forward Host header; remove stale ngrok hostname |
| `SIGNALDECK_WEB_ORIGINS` | Origin list for CORS; daemon refuses to boot without it on public deployments |
| `SIGNALDECK_PUBLIC_SURFACE=1` | Enables public surface routing |
| `SIGNALDECK_PUBLIC_READS=false` | Keeps reads private |
| `SIGNALDECK_OPEN_SIGNUP=false` | Disables public signup |

Restart daemon after changes:

```bash
bash ops/signaldeck-ctl.sh deploy
```

---

## 6 Rebuild the web app

Set build-time env vars and rebuild:

```bash
export NEXT_PUBLIC_SITE_URL=https://<hostname>
export NEXT_PUBLIC_SIGNALDECK_PUBLIC=1
bash ops/signaldeck-ctl.sh deploy   # builds web with the exported vars, restarts both processes
```

If you build into an alternate directory for a dry run, the variable is `SIGNALDECK_DIST_DIR` (not `NEXT_DIST_DIR`), and `next build` will rewrite `web/tsconfig.json` — revert that file afterwards; it is the tool, not an edit.

- `NEXT_PUBLIC_SIGNALDECK_PUBLIC=1` is **required**; omitting it redirects all anonymous visitors to `/login`
- These are inlined at build time; cannot be set at runtime
- `SIGNALDECK_DIST_DIR` (not `NEXT_DIST_DIR`) controls the output directory

---

## 7 Verify from outside

**Must test from a device NOT on the same network (phone on mobile data).**

| Endpoint | Expected |
|----------|----------|
| `GET https://<hostname>/` | 200, **no redirect to /login** |
| `GET https://<hostname>/proof` | 200 |
| `GET https://<hostname>/accuracy` | 200 |
| `GET https://<hostname>/volatility` | 200 |
| `GET https://<hostname>/api/export/bars.csv?symbol=AAPL` | 451 |
| `GET https://<hostname>/api/health` | JSON with `"degraded":true` and `"openSignup":false` |

Version parity check:

```bash
# On the server
git rev-parse HEAD

# From outside
curl -s https://<hostname>/api/version | jq -r .revision
```

Both must match exactly.

---

## 8 Rollback

```powershell
schtasks /End /TN "SignalDeck Cloudflare Tunnel"
```

Edit `daemon/.env`:
- Remove `<hostname>` from `SIGNALDECK_ALLOWED_HOSTS` (keep the loopback entries)
- Optionally clear `SIGNALDECK_WEB_ORIGINS` and `SIGNALDECK_ASSUME_TUNNEL`

```bash
bash ops/signaldeck-ctl.sh deploy
```

Site becomes unreachable within seconds. No other changes.