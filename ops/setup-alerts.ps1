#Requires -Version 5.1

<#
.SYNOPSIS
Configure ONE off-machine alert transport for SignalDeck, then prove it delivers.

.DESCRIPTION
SignalDeck raises alerts but ships with no REMOTE transport configured. A local
Windows toast is built in and always works, but it only reaches someone sitting
at this desk — so logs/backup-offline.log has said, correctly, every night:

  NOTIFY: SignalDeck: alerts are SILENT beyond this machine … daemon alerts
  stay local and go unseen when nobody is at the keyboard.

All five remote transports are implemented in internal/notify. The only missing
piece is a credential, which only the operator can create. This script takes
that credential, appends it to daemon\.env, and then — after a restart — proves
end to end that it delivers.

ORDER MATTERS. Writing to daemon\.env changes nothing until the daemon re-reads
it, so this script deliberately REFUSES to test in the same run that it writes.
Testing before the restart would report success for configuration that is not
loaded, which is the one failure mode a setup script must not have.

.PARAMETER Discord
Discord webhook URL. Server Settings -> Integrations -> Webhooks -> Copy URL.
.PARAMETER TelegramToken
Telegram bot token from @BotFather. Requires -TelegramChat.
.PARAMETER TelegramChat
Telegram chat id. Message the bot once, then read it from
https://api.telegram.org/bot<TOKEN>/getUpdates
.PARAMETER Slack
Slack Incoming Webhook URL.
.PARAMETER Webhook
Any HTTP endpoint that accepts a JSON POST (Pushover, ntfy, your own).
.PARAMETER SmtpHost
SMTP server hostname. Requires the other -Smtp* parameters.
.PARAMETER SmtpPort
SMTP port, e.g. 587.
.PARAMETER SmtpUser
SMTP username.
.PARAMETER SmtpPass
SMTP password. Use an APP PASSWORD, never your account password.
.PARAMETER SmtpFrom
Sender address.
.PARAMETER SmtpTo
Recipient address.
.PARAMETER Test
Send a real test message through every CONFIGURED transport and report the
result. Changes nothing. Run this AFTER deploying.

.EXAMPLE
.\setup-alerts.ps1 -Discord "https://discord.com/api/webhooks/123/abc"
bash ops/signaldeck-ctl.sh deploy
.\setup-alerts.ps1 -Test
#>

param(
    [string]$Discord,
    [string]$TelegramToken,
    [string]$TelegramChat,
    [string]$Slack,
    [string]$Webhook,
    [string]$SmtpHost,
    [int]$SmtpPort,
    [string]$SmtpUser,
    [string]$SmtpPass,
    [string]$SmtpFrom,
    [string]$SmtpTo,
    [switch]$Test
)

$ErrorActionPreference = 'Stop'

$rootDir = Split-Path -Path $PSScriptRoot -Parent
$envFile = Join-Path -Path $rootDir -ChildPath 'daemon\.env'

function Show-Menu {
    Write-Host @'
SignalDeck — off-machine alert setup

Alerts currently reach the local desktop only. Pick ONE transport, run its
command, deploy, then verify. All five are already implemented; only the
credential is missing.

  Discord   (easiest — Server Settings > Integrations > Webhooks > Copy URL)
    .\setup-alerts.ps1 -Discord "https://discord.com/api/webhooks/ID/TOKEN"

  Telegram  (@BotFather > /newbot; then message the bot and read the chat id
             from https://api.telegram.org/bot<TOKEN>/getUpdates)
    .\setup-alerts.ps1 -TelegramToken "123456:AA..." -TelegramChat "123456789"

  Slack     (Incoming Webhooks app > Add to Workspace)
    .\setup-alerts.ps1 -Slack "https://hooks.slack.com/services/T00/B00/XXX"

  Webhook   (Pushover, ntfy, or anything that takes a JSON POST)
    .\setup-alerts.ps1 -Webhook "https://example.com/your-endpoint"

  Email     (SmtpPass must be an APP password, not your account password)
    .\setup-alerts.ps1 -SmtpHost smtp.example.com -SmtpPort 587 `
      -SmtpUser you@example.com -SmtpPass app-password `
      -SmtpFrom you@example.com -SmtpTo you@example.com

Then, in order:
  bash ops/signaldeck-ctl.sh deploy      # daemon/.env is only read at startup
  .\setup-alerts.ps1 -Test               # proves it actually delivers

A transport that has never delivered a test message is not configured, it is
merely spelled.
'@
}

function Invoke-DeliveryTest {
    Write-Host ''
    Write-Host 'Sending a real test message through every configured transport...'

    if (-not (Test-Path -Path $envFile)) {
        Write-Host "ERROR: $envFile not found." -ForegroundColor Red
        return 1
    }

    # The endpoint is CSRF-guarded AND authenticated: anonymous reads are closed
    # once the Host allowlist names a public hostname, so both headers are
    # required. A missing token is a 401, a missing header is a 403.
    $apiToken = $null
    Get-Content -Path $envFile | ForEach-Object {
        if ($_ -match '^SIGNALDECK_API_TOKEN=(.+)$') { $apiToken = $matches[1].Trim() }
    }
    if (-not $apiToken) {
        Write-Host "ERROR: SIGNALDECK_API_TOKEN not found in $envFile." -ForegroundColor Red
        Write-Host '  Generate one and add it, or POST /api/notify/test will 401.' -ForegroundColor Yellow
        return 1
    }

    try {
        $headers = @{ 'X-Signaldeck' = '1'; 'Authorization' = "Bearer $apiToken" }
        $resp = Invoke-RestMethod -Method Post -Headers $headers `
            -Uri 'http://127.0.0.1:8322/api/notify/test' -TimeoutSec 30

        # HTTP 200 does NOT mean a message went anywhere. This endpoint answers
        # {"sent":false,"reason":...} when no REMOTE transport is configured, so
        # reporting the 200 as success is exactly the false-green this script
        # exists to prevent.
        if (-not $resp.sent) {
            Write-Host 'NOT SENT: nothing left this machine.' -ForegroundColor Red
            if ($resp.reason) { Write-Host "  $($resp.reason)" -ForegroundColor Yellow }
            Write-Host '  Alerts still reach the local Windows toast only.' -ForegroundColor Yellow
            return 1
        }

        Write-Host "SENT via: $($resp.transports -join ', ')" -ForegroundColor Green
        Write-Host 'Now go look at your phone. Delivery is NOT tracked - the daemon' -ForegroundColor Yellow
        Write-Host 'can only report that it handed the message off. If nothing arrived,' -ForegroundColor Yellow
        Write-Host 'the credential is wrong; check GET /api/notify-status for lastError.' -ForegroundColor Yellow
        return 0
    } catch {
        $status = $null
        if ($_.Exception.Response) { $status = $_.Exception.Response.StatusCode.value__ }
        Write-Host "FAILED: $($_.Exception.Message)" -ForegroundColor Red
        switch ($status) {
            401 { Write-Host '  401 — SIGNALDECK_API_TOKEN is wrong, or the daemon has not re-read it. Deploy first.' }
            403 { Write-Host '  403 — the X-Signaldeck CSRF header was rejected, or the Host is not allowlisted.' }
            429 { Write-Host '  429 — rate limited; wait a second and retry.' }
            default {
                Write-Host '  No HTTP status: the daemon is probably not running on 127.0.0.1:8322.'
                Write-Host '  Start it with: bash ops/signaldeck-ctl.sh up'
            }
        }
        return 1
    }
}

# -Test is a pure verification run: it selects no transport and writes nothing.
# Checked BEFORE transport selection, or the "no transport specified" branch
# below would reject it.
if ($Test) { exit (Invoke-DeliveryTest) }

$varsToWrite = @()
if ($Discord) {
    $varsToWrite += @{ Name = 'SIGNALDECK_DISCORD_WEBHOOK'; Value = $Discord }
} elseif ($TelegramToken -or $TelegramChat) {
    if (-not ($TelegramToken -and $TelegramChat)) {
        Write-Host 'ERROR: Telegram needs BOTH -TelegramToken and -TelegramChat.' -ForegroundColor Red
        exit 1
    }
    $varsToWrite += @{ Name = 'SIGNALDECK_TELEGRAM_BOT_TOKEN'; Value = $TelegramToken }
    $varsToWrite += @{ Name = 'SIGNALDECK_TELEGRAM_CHAT_ID';   Value = $TelegramChat }
} elseif ($Slack) {
    $varsToWrite += @{ Name = 'SIGNALDECK_SLACK_WEBHOOK'; Value = $Slack }
} elseif ($Webhook) {
    $varsToWrite += @{ Name = 'SIGNALDECK_WEBHOOK_URL'; Value = $Webhook }
} elseif ($SmtpHost) {
    # Ordered deliberately: a hashtable's key order is not stable, and an
    # operator reading .env should see the SMTP block in a sensible sequence.
    $smtp = [ordered]@{
        SIGNALDECK_SMTP_HOST = $SmtpHost
        SIGNALDECK_SMTP_PORT = "$SmtpPort"
        SIGNALDECK_SMTP_USER = $SmtpUser
        SIGNALDECK_SMTP_PASS = $SmtpPass
        SIGNALDECK_SMTP_FROM = $SmtpFrom
        SIGNALDECK_SMTP_TO   = $SmtpTo
    }
    # Validate EVERY field before writing ANY of them — a half-written SMTP
    # block is a transport that looks configured and cannot deliver.
    foreach ($k in $smtp.Keys) {
        if (-not $smtp[$k] -or $smtp[$k] -eq '0') {
            Write-Host "ERROR: SMTP needs all of -SmtpHost -SmtpPort -SmtpUser -SmtpPass -SmtpFrom -SmtpTo (missing: $k)" -ForegroundColor Red
            exit 1
        }
    }
    foreach ($k in $smtp.Keys) { $varsToWrite += @{ Name = $k; Value = $smtp[$k] } }
} else {
    Show-Menu
    exit 0
}

# Refuse to duplicate a key rather than appending a second one: config.Load
# takes the FIRST match, so a duplicate silently keeps the old value and the
# operator would be debugging a credential the daemon never read.
if (Test-Path -Path $envFile) {
    $existing = Get-Content -Path $envFile
    foreach ($v in $varsToWrite) {
        $pattern = '^' + [regex]::Escape($v.Name) + '='
        if ($existing | Where-Object { $_ -match $pattern }) {
            Write-Host "ERROR: $($v.Name) is already set in $envFile." -ForegroundColor Red
            Write-Host '  Edit it there directly — appending a duplicate would be ignored.' -ForegroundColor Yellow
            exit 1
        }
    }
}

# APPEND only. daemon\.env holds the LLM key, the TV webhook secret and the API
# token; rewriting the file would destroy them.
Write-Host "Appending to $envFile"
Add-Content -Path $envFile -Value ''
Add-Content -Path $envFile -Value "# Alert transport configured by ops\setup-alerts.ps1 on $(Get-Date -Format 'yyyy-MM-dd')."
foreach ($v in $varsToWrite) {
    Add-Content -Path $envFile -Value "$($v.Name)=$($v.Value)"
    Write-Host "  added $($v.Name)"
}

Write-Host ''
Write-Host 'Written. Two steps remain, IN THIS ORDER:' -ForegroundColor Green
Write-Host '  1. bash ops/signaldeck-ctl.sh deploy    # daemon/.env is read only at startup' -ForegroundColor Cyan
Write-Host '  2. .\setup-alerts.ps1 -Test             # proves it actually delivers' -ForegroundColor Cyan
Write-Host ''
Write-Host 'This script does NOT test now, on purpose: the daemon is still running the' -ForegroundColor Yellow
Write-Host 'old configuration, so a test here would report success for a transport that' -ForegroundColor Yellow
Write-Host 'is not loaded. Until step 2 passes, treat this transport as unconfigured.' -ForegroundColor Yellow
