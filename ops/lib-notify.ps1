# Shared alerting for the PowerShell health checks.
#
# WHY THIS EXISTS. ops/check-grader-health.ps1 and ops/check-task-health.ps1
# both detect real faults and both only ever wrote to the console. Under Task
# Scheduler the console goes nowhere, so an unhealthy result became a
# LastTaskResult=1 sitting in a UI nobody opens: measured 2026-09-12, both tasks
# had been red since 2026-09-10 and nothing had surfaced it. A check that
# detects a fault and tells no one is not a check.
#
# This is the PowerShell counterpart to sd_notify in ops/lib-portable.sh and
# keeps the same contract, deliberately: try the desktop channel, and ALWAYS
# write the log line. The log is the point -- it is the one channel that works
# under a scheduled task with no interactive session, so a missing or broken
# notifier can never make an alert vanish.

function Send-SdAlert {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Title,
        [Parameter(Mandatory = $true)][string]$Body,
        [Parameter(Mandatory = $true)][string]$Repo
    )

    $stamp = (Get-Date).ToString('yyyy-MM-ddTHH:mm:ss')
    $line = "$stamp NOTIFY: $Title - $Body"

    # The log first, and never conditionally. If the balloon below throws or the
    # session is non-interactive, the alert is still on disk.
    $logDir = Join-Path $Repo 'logs'
    if (-not (Test-Path -LiteralPath $logDir)) {
        New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    }
    $logFile = Join-Path $logDir 'health-alerts.log'
    # Two health tasks can fire in the same minute and the daemon writes here
    # too, so a single Add-Content can lose to a share violation. Retry briefly
    # rather than let a locked file swallow the alert; on total failure fall
    # back to stderr, which at least reaches a task's captured output.
    $wrote = $false
    foreach ($attempt in 1..5) {
        try {
            Add-Content -LiteralPath $logFile -Value $line -ErrorAction Stop
            $wrote = $true
            break
        } catch {
            Start-Sleep -Milliseconds (100 * $attempt)
        }
    }
    if (-not $wrote) {
        [Console]::Error.WriteLine("ALERT (log write failed): $line")
    }

    # Balloon tip, best effort. Non-modal on purpose: an unattended scheduled
    # run must never be left blocking on a dialog nobody will click. Wrapped
    # because there is no desktop session under a service principal, and a
    # throw here must not fail the health check that called it.
    try {
        Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop
        Add-Type -AssemblyName System.Drawing -ErrorAction Stop
        $icon = New-Object System.Windows.Forms.NotifyIcon
        $icon.Icon = [System.Drawing.SystemIcons]::Warning
        $icon.BalloonTipTitle = $Title
        $icon.BalloonTipText = $Body
        $icon.Visible = $true
        $icon.ShowBalloonTip(10000)
        Start-Sleep -Seconds 1
        $icon.Dispose()
    } catch {
        # Recorded, not raised: the log line above already carried the alert.
        [Console]::Error.WriteLine("desktop notification unavailable: $($_.Exception.Message)")
    }
}
