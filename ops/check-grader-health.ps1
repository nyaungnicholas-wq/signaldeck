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
    # 26 hours, not 2. The accuracy grader runs ONCE A DAY (14:05 PT, task
    # "SignalDeck Accuracy"), so a 120-minute ceiling reported "unhealthy" for
    # roughly 22 hours out of every 24 — including right now, with a clean
    # daily success on 08-06/07/08/09 in grader_heartbeats. A check that is red
    # almost all the time cannot be wired to alerting, which is why nothing
    # invokes this script and why the 33-hour silent refusal it was written to
    # catch would still go unnoticed. 26h = one full cadence plus two hours of
    # slack for a late or long run; a genuinely skipped day still trips it.
    [int]$MaxAgeMinutes = 1560,
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

# LEDGER REVISIONS. tools/accuracy_registry.py strips a whole family's verdict
# when any post-epoch row cites a commit git cannot resolve. A commit reachable
# from no ref still resolves until the first `git gc` after its reflog entry
# expires (~30 days), so a deleted ref strips verdicts silently weeks later,
# and ops/githooks/reference-transaction only sees ref moves, never gc or a
# deletion made in another clone. Measured 2026-09-10: b84670c9 (252 1w rows,
# 7 liquidity21-crypto, 7 trend21-crypto) resolves and nothing reaches it. So
# the ledger is asked daily, here, while the commit can still be re-attached.
# Read-only (sqlite mode=ro). Exit 1 names each revision and the families it
# takes with it; the tool prints to stdout only, so no redirect is needed and
# a crash traceback still reaches the task log via *>>.
$lrr = Join-Path $repo 'tools\ledger_revision_reachability.py'
$lrrOut = @(& python $lrr)
if ($LASTEXITCODE -ne 0) {
    Write-Output 'LEDGER REVISIONS: BROKEN'
    $lrrOut | ForEach-Object { Write-Output "  $_" }
    $failed = $true
} else {
    Write-Output ('LEDGER REVISIONS: ' + ($lrrOut | Select-Object -Last 1))
}

if ($failed) { Write-Output 'RESULT: unhealthy'; exit 1 }
Write-Output 'RESULT: ok'
exit 0
