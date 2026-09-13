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
# Every finding is written AND recorded through one call. The two used to be
# separate lines at eight sites, which is how the alert body below could have
# ended up empty: there was no variable holding what went wrong, only console
# text nobody reads under Task Scheduler.
$reasons = @()
function Set-Unhealthy {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Output $Message
    $script:reasons += $Message
    $script:failed = $true
}
# Alerting. Without this the script detected faults and told nobody: under Task
# Scheduler its console output goes nowhere, so an unhealthy run was a
# LastTaskResult=1 in a UI no one opens. Both health tasks sat red from
# 2026-09-10 to 2026-09-12 exactly that way.
. (Join-Path $PSScriptRoot 'lib-notify.ps1')

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
        Set-Unhealthy "HEARTBEAT: database not found at $dbPath"
    } else {
        $out = & python $tmp $dbPath 2>&1
        if ($LASTEXITCODE -ne 0) {
            Set-Unhealthy "HEARTBEAT: unreadable ($out)"
        } else {
            $line = ($out | Where-Object { $_ -match '\S' } | Select-Object -Last 1)
            if ($line -eq 'NONE') {
                Set-Unhealthy 'HEARTBEAT: none recorded'
            } elseif ($line -match '^([^|]*)\|([^|]*)\|(.*)$') {
                $success    = $matches[1]
                $finishedAt = $matches[2]
                $lastErr    = $matches[3]
                if ($success -ne '1') {
                    Set-Unhealthy "HEARTBEAT: last run FAILED: $lastErr"
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
                        Set-Unhealthy "HEARTBEAT: timestamp unparseable: $finishedAt"
                    } else {
                        $age = ([datetime]::UtcNow - $finished).TotalMinutes
                        if ($age -gt $MaxAgeMinutes) {
                            Set-Unhealthy ("HEARTBEAT: STALE ({0:N1} min, max {1})" -f $age, $MaxAgeMinutes)
                        } else {
                            Write-Output ("HEARTBEAT: fresh ({0:N1} min)" -f $age)
                        }
                    }
                }
            } else {
                Set-Unhealthy "HEARTBEAT: unexpected output: $line"
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
        Set-Unhealthy ("WAL: CRITICAL ({0:N1} MB)" -f $walMB)
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
    Set-Unhealthy 'LEDGER REVISIONS: BROKEN'
    $lrrOut | ForEach-Object { Write-Output "  $_" }
} else {
    Write-Output ('LEDGER REVISIONS: ' + ($lrrOut | Select-Object -Last 1))
}

# LEDGER MANIFEST. ops/ledger-revisions.txt is all CI's provenance job can
# enforce, and it is regenerated only when someone runs
# `bash ops/ledger-provenance.sh --write` here, on the machine with the
# database. It went 36 days stale, 2026-08-04 to 2026-09-09: 101 of the 108
# ledger revisions on the remote had no CI protection the whole time, and CI
# stayed green because the seven it did record stayed reachable. --diff
# recomputes what --write would record and compares it with the committed
# file. Exit 1 is a warning, printed in full and not fatal: a revision cited
# by the ledger and on the remote for 7+ days is unrecorded (the weekly chore
# is due), or a recorded one is no longer recordable (CI is about to fail and
# --check's message says what to do). Exit 2 means it could not run, which on
# the machine that holds the database is unhealthy. No stderr redirect, same
# as the python call above: --diff writes everything to stdout. PATH's
# bash.exe is the WSL launcher on this box, so Git's is resolved explicitly
# the way daemon-guard.ps1 does; a missing one is reported, not failed.
$bash = @($env:ProgramFiles, ${env:ProgramFiles(x86)}) |
    Where-Object { $_ } |
    ForEach-Object { Join-Path $_ 'Git\bin\bash.exe' } |
    Where-Object { Test-Path -LiteralPath $_ } |
    Select-Object -First 1
if (-not $bash) {
    Write-Output 'LEDGER MANIFEST: SKIPPED (no Git bash under ProgramFiles)'
} else {
    $lpOut = @(& $bash ((Join-Path $repo 'ops\ledger-provenance.sh') -replace '\\', '/') --diff)
    $lpExit = $LASTEXITCODE
    Write-Output ('LEDGER MANIFEST: ' + ($lpOut | Select-Object -Last 1))
    if ($lpExit -ne 0) { $lpOut | Select-Object -SkipLast 1 | ForEach-Object { Write-Output "  $_" } }
    if ($lpExit -gt 1) { Set-Unhealthy ('LEDGER MANIFEST: ' + ($lpOut | Select-Object -Last 1)) }
}


# LEDGER COVERAGE. A served forecast that never reached the hash chain cannot be
# proven un-backdated, and nothing measured how many there were.
#
# The write is four separate transactions -- UpsertPrediction, then
# SeedBenchmarkOutcome, then AppendLedger, each with its own BeginTx -- so a
# process killed between the first and the third leaves a served prediction with
# no ledger entry. That window is NOT covered by the append's own error handling:
# measured 2026-09-12, there are 0 ledger_append_error dq events in 14 days and 1
# ever, while 30 served predictions have no ledger row. Nothing failed; the
# process died mid-sequence and there was no error to record.
#
# n_used > 0 is load-bearing. internal/pipeline/predict_evidence.go writes
# evidence-only rows with NUsed=0, documented "never graded, never served", one
# per symbol per trading day so the coverage monitor has a full-universe
# denominator. Those are CORRECTLY absent from the ledger, which commits served
# forecasts. Counting them makes this read 56,778 instead of 30 -- a false crisis
# off by three orders of magnitude, which is what the first pass at this check
# reported before the denominator was checked.
#
# Reported, never back-filled. Appending an entry now for a prediction made days
# ago would stamp a later predicted_at on an older bar, manufacturing exactly the
# anteriority the chain exists to prove. These 30 stay uncommitted and counted.
$maxUnledgered = 50   # measured 30 on 2026-09-12; ratchet DOWN, never up
$pyLedger = @'
import sqlite3, sys

LEDGER_START = 1783155600  # first prediction_ledger bar_ts; nothing before it was ever ledgered
conn = None
try:
    conn = sqlite3.connect("file:" + sys.argv[1] + "?mode=ro", uri=True)
    n = conn.execute(
        "select count(*) from predictions p "
        "left join prediction_ledger l on l.symbol_id=p.symbol_id "
        "  and l.horizon=p.horizon and l.bar_ts=p.ts "
        "where l.seq is null and p.n_used > 0 and p.ts >= ?",
        (LEDGER_START,)).fetchone()[0]
    total = conn.execute(
        "select count(*) from predictions where n_used > 0 and ts >= ?",
        (LEDGER_START,)).fetchone()[0]
    print("%d|%d" % (n, total))
except sqlite3.OperationalError as e:
    print("SKIP:%s" % e)
except Exception as e:
    print("ERROR:%s" % e, file=sys.stderr)
    sys.exit(2)
finally:
    if conn is not None:
        conn.close()
'@

$tmpL = Join-Path ([System.IO.Path]::GetTempPath()) ("sd_ledgercov_{0}.py" -f [guid]::NewGuid().ToString('N'))
try {
    Set-Content -LiteralPath $tmpL -Value $pyLedger -Encoding ASCII
    $covOut = (& python $tmpL $dbPath 2>&1 | Where-Object { $_ -match '\S' } | Select-Object -Last 1)
    if ($LASTEXITCODE -ne 0) {
        Write-Output "LEDGER COVERAGE: unreadable ($covOut)"
    } elseif ($covOut -like 'SKIP:*') {
        Write-Output "LEDGER COVERAGE: SKIPPED ($covOut)"
    } elseif ($covOut -match '^(\d+)\|(\d+)$') {
        $unledgered = [int]$matches[1]
        $servedTot = [int]$matches[2]
        if ($unledgered -gt $maxUnledgered) {
            Set-Unhealthy ("LEDGER COVERAGE: {0} served prediction(s) of {1} never reached the chain (max {2})" -f $unledgered, $servedTot, $maxUnledgered)
        } else {
            Write-Output ("LEDGER COVERAGE: {0} unledgered of {1} served (max {2})" -f $unledgered, $servedTot, $maxUnledgered)
        }
    } else {
        Write-Output "LEDGER COVERAGE: unexpected output ($covOut)"
    }
} finally {
    Remove-Item -LiteralPath $tmpL -ErrorAction SilentlyContinue
}

if ($failed) {
    Write-Output 'RESULT: unhealthy'
    # The captured findings ARE the alert body: a page saying only "grader
    # unhealthy" sends the reader back to the console output that started this
    # problem. Joined onto one line so the log stays one record per event.
    $why = ($reasons -join '; ')
    if (-not $why) { $why = 'see the run output' }
    Send-SdAlert -Title 'SignalDeck grader health: UNHEALTHY' -Body $why -Repo $repo
    exit 1
}
Write-Output 'RESULT: ok'
exit 0
