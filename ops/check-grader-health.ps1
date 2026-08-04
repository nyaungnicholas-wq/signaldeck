<#
Grader heartbeat + WAL health check.

WHY THIS EXISTS. A scheduled task exiting 0 is not evidence that the grade ran.
On 2026-08-03 the Windows task reported success while the accuracy grader had
been refusing for 33 hours: the process started, did nothing useful, and exited
cleanly. Task exit codes describe the wrapper; the heartbeat describes the work.

Every unknown resolves to unhealthy. A missing heartbeat, a failed run, an
unparseable timestamp and an unreadable database all exit non-zero, because the
one state this check must never report is "fine, probably".

  .\check-grader-health.ps1
  .\check-grader-health.ps1 -MaxAgeMinutes 180
#>
param(
    [int]$MaxAgeMinutes = 120,
    [int]$WalWarnMB = 128,
    [int]$WalCritMB = 256
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$failed = $false

# The sqlite3 CLI is not installed on this machine and never has been; the repo
# reads SQLite through Python everywhere for exactly that reason.
$py = @'
import sqlite3, sys

conn = None
try:
    conn = sqlite3.connect("file:" + sys.argv[1] + "?mode=ro", uri=True)
    row = conn.execute(
        "select success, finished_at, coalesce(error,'') from grader_heartbeats "
        "where task='accuracy_registry' order by finished_at desc limit 1"
    ).fetchone()
    print("%s|%s|%s" % row if row else "NONE")
except sqlite3.OperationalError:
    # No such table yet: the heartbeat has never been written. That is a
    # reportable state, not a crash.
    print("NONE")
except Exception as e:
    print("ERROR:%s" % e, file=sys.stderr)
    sys.exit(2)
finally:
    if conn is not None:
        conn.close()
'@

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("sd_heartbeat_{0}.py" -f [guid]::NewGuid().ToString('N'))
try {
    Set-Content -LiteralPath $tmp -Value $py -Encoding UTF8
    $dbPath = Join-Path $repo 'data\signaldeck.db'

    if (-not (Test-Path -LiteralPath $dbPath)) {
        Write-Output "HEARTBEAT: database not found at $dbPath"
        $failed = $true
    } else {
        $out = & python $tmp $dbPath 2>&1
        if ($LASTEXITCODE -ne 0) {
            Write-Output "HEARTBEAT: unreadable ($out)"
            $failed = $true
        } else {
            $line = ($out | Where-Object { $_ -match '\S' } | Select-Object -Last 1)
            if ($line -eq 'NONE') {
                Write-Output 'HEARTBEAT: none recorded'
                $failed = $true
            } elseif ($line -match '^([^|]*)\|([^|]*)\|(.*)$') {
                $success    = $matches[1]
                $finishedAt = $matches[2]
                $lastErr    = $matches[3]
                if ($success -ne '1') {
                    Write-Output "HEARTBEAT: last run FAILED: $lastErr"
                    $failed = $true
                } else {
                    # AssumeUniversal so a timestamp carrying no offset is still
                    # read as UTC; AdjustToUniversal so one that DOES carry an
                    # offset is normalised rather than shifted into local time.
                    $styles = [System.Globalization.DateTimeStyles]::AssumeUniversal -bor `
                              [System.Globalization.DateTimeStyles]::AdjustToUniversal
                    [datetime]$finished = [datetime]::MinValue
                    $parsed = [datetime]::TryParse(
                        $finishedAt, [System.Globalization.CultureInfo]::InvariantCulture,
                        $styles, [ref]$finished)
                    if (-not $parsed) {
                        Write-Output "HEARTBEAT: timestamp unparseable: $finishedAt"
                        $failed = $true
                    } else {
                        $age = ([datetime]::UtcNow - $finished).TotalMinutes
                        if ($age -gt $MaxAgeMinutes) {
                            Write-Output ("HEARTBEAT: STALE ({0:N1} min, max {1})" -f $age, $MaxAgeMinutes)
                            $failed = $true
                        } else {
                            Write-Output ("HEARTBEAT: fresh ({0:N1} min)" -f $age)
                        }
                    }
                }
            } else {
                Write-Output "HEARTBEAT: unexpected output: $line"
                $failed = $true
            }
        }
    }
} finally {
    if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue }
}

# WAL. store.Open already caps this file at 64MB via journal_size_limit, so a
# WAL above the warn threshold means checkpoints are being STARVED by long-lived
# readers, not that the bound is missing.
$walPath = Join-Path $repo 'data\signaldeck.db-wal'
if (-not (Test-Path -LiteralPath $walPath)) {
    Write-Output 'WAL: absent (idle)'
} else {
    $walMB = (Get-Item -LiteralPath $walPath).Length / 1MB
    if ($walMB -gt $WalCritMB) {
        Write-Output ("WAL: CRITICAL ({0:N1} MB)" -f $walMB)
        $failed = $true
    } elseif ($walMB -gt $WalWarnMB) {
        Write-Output ("WAL: warn ({0:N1} MB)" -f $walMB)
    } else {
        Write-Output ("WAL: ok ({0:N1} MB)" -f $walMB)
    }
}

if ($failed) { Write-Output 'RESULT: unhealthy'; exit 1 }
Write-Output 'RESULT: ok'
exit 0
