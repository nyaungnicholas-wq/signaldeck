<#
.SYNOPSIS
  Overnight accuracy loop. Runs until stopped, with ZERO Claude calls at runtime.

.DESCRIPTION
  A thin supervisor over ops/selfimprove-loop.ps1. It does four things per round:

    1. REPORT   -- ops/accuracy_baseline.py --report, appended to the journal, so
                  every round leaves the measured number on the record whether it
                  moved or not.
    2. REFILL   -- if the backlog has no unchecked items left, brief a local worker
                  to propose more. Proposals are FORMAT-VALIDATED before they are
                  appended; an item without a runnable `verify:` is a wish, and the
                  loop drops it rather than pretending to work it.
    3. WORK     -- hand a bounded chunk of wall clock to selfimprove-loop.ps1, which
                  owns the gate/brief/verify/commit cycle already.
    4. CHECK    -- kill file, then wall-clock cap.

  What this deliberately does NOT do: touch the Go research loop. Its Bonferroni
  divisor counts every search ever run and is persisted for the system's lifetime,
  so extra runs permanently raise the bar for every future search. That loop is
  day-gated on purpose. Overnight router work belongs here and in eighty-loop.ps1,
  neither of which spends a family-wise alpha budget.

  Stop it by creating the kill file (default ops/STOP-OVERNIGHT) -- the round in
  flight finishes and the loop exits cleanly. Ctrl-C works too, but the kill file
  is safe to use from another session mid-cycle.

.EXAMPLE
  pwsh -File ops/overnight.ps1
  pwsh -File ops/overnight.ps1 -Hours 8 -CycleMinutes 30
#>
param(
  [double]$Hours = 0,                              # 0 = run until stopped
  [int]$CycleMinutes = 45,
  [string]$KillFile = 'ops/STOP-OVERNIGHT',
  [string]$Backlog = 'ops/IMPROVE_BACKLOG.md',
  [string]$Journal = 'logs/overnight.md',
  [string]$Events = 'logs/overnight.jsonl',
  [string]$Db = 'data/signaldeck.db',
  [switch]$SelfTest
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo
$omni = Join-Path $env:USERPROFILE '.claude\scripts\omni.ps1'

function Note([string]$kind, [string]$msg) {
  $stamp = (Get-Date).ToUniversalTime().ToString('o')
  $line = [pscustomobject]@{ ts = $stamp; kind = $kind; msg = $msg } | ConvertTo-Json -Compress
  for ($i = 0; $i -lt 5; $i++) {
    try { Add-Content -Path $Events -Value $line -ErrorAction Stop; break }
    catch { Start-Sleep -Milliseconds 200 }        # another session holds the file
  }
  Write-Host "[$kind] $msg"
}

function Journal([string]$text) {
  for ($i = 0; $i -lt 5; $i++) {
    try { Add-Content -Path $Journal -Value $text -ErrorAction Stop; break }
    catch { Start-Sleep -Milliseconds 200 }
  }
}

function OpenItems { @(Select-String -Path $Backlog -Pattern '^## \[ \]' -AllMatches).Count }

# An item is workable only if it carries a command that can fail. Anything else
# would let the loop tick itself off against nothing.
function ValidProposal([string]$text) {
  $hasHead = $text -match '(?m)^## \[ \] \S'
  $hasVerify = $text -match '(?m)^verify: `[^`]+`\s*$'
  $hasFiles = $text -match '(?m)^files: `[^`]+`\s*$'
  return ($hasHead -and $hasVerify -and $hasFiles)
}

function AccuracyReport {
  try { & python ops/accuracy_baseline.py --report --db $Db 2>&1 | Out-String }
  catch { "accuracy report failed: $_" }
}

function Refill {
  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) "backlog-proposal-$PID.md"
  $brief = @"
You are proposing work items for an autonomous improvement loop on a stock-forecasting
system written in Go (daemon/) with Python analysis tools (tools/, ops/).

MEASURED STATE (independent symbol-days, deduped one per symbol/horizon/UTC-day):
  1d directional accuracy 46.41% vs folded naive baseline 57.46%  -> lift -11.05pp
  1w directional accuracy 49.20% vs folded naive baseline 51.41%  -> lift  -2.21pp
  Known mechanism: on 5 of the last 10 days the whole ~328-symbol cross-section was
  called the same direction (up-call rate 0.012 to 0.991), so one market call is
  published as ~328 forecasts. The structural surface (trend21/vol21/liquidity21)
  has 34,305 forecasts and 0 graded, because its 21-day horizons do not mature
  until 2026-08-17 -- nothing can grade them sooner.

GOAL: +10% RELATIVE accuracy at FIXED COVERAGE.

Propose 3 improvement items. Output ONLY markdown, no preamble, no code fences.
Each item EXACTLY in this shape:

## [ ] <one-line title>

<2-6 lines: the defect, the measurement that shows it, and what to change>

verify: ``<a shell command that exits non-zero until the item is genuinely done>``
files: ``<the most likely file path to change>``

HARD RULES:
- Every verify MUST be a real runnable command: a `go test ./... -run TestName -v`
  piped to grep for its PASS line, or a `python ops/...` check. Never a command
  that passes vacuously (a `go test -run` matching no test EXITS 0 -- always grep
  for the PASS line).
- Never propose raising accuracy by abstaining, by narrowing the graded subset,
  by dropping days, or by widening a threshold band. Those raise the number
  without improving the forecast and will be rejected.
- Never propose changing the research loop's cadence, its grid size, or its alpha.
- Prefer items whose verify can go green from a code change tonight over items
  that can only go green as new market days accrue.
"@
  Note 'refill' 'backlog empty -- briefing a worker for proposals'
  try {
    & powershell -NoProfile -File $omni -Task reason -Prompt $brief -Out $tmp -TimeoutSec 600 2>&1 | Out-Null
  } catch {
    Note 'refill-error' "worker call failed: $_"; return 0
  }
  if (-not (Test-Path $tmp)) { Note 'refill-error' 'worker produced no file'; return 0 }
  $text = Get-Content $tmp -Raw
  Remove-Item $tmp -ErrorAction SilentlyContinue
  if (-not (ValidProposal $text)) {
    Note 'refill-reject' 'proposal lacked a runnable verify: / files: line -- dropped'
    return 0
  }
  Add-Content -Path $Backlog -Value "`n$text`n"
  $n = OpenItems
  Note 'refill-ok' "appended proposals; $n open items now"
  return 1
}

# ---- self test: exercises the logic that decides whether to keep going ----
if ($SelfTest) {
  $ok = $true
  $bt = [char]96          # a literal backtick; escaping one inside "" is a trap
  $good = @('## [ ] Do a thing', '', 'Because of a measured defect.', '',
            "verify: ${bt}python ops/accuracy_gates.py xsection${bt}",
            "files: ${bt}daemon/x.go${bt}") -join "`n"
  $noVerify = @('## [ ] Do a thing', '', 'Because.', '',
                "files: ${bt}daemon/x.go${bt}") -join "`n"
  $notItem = "Just some prose with verify: ${bt}true${bt} in it."
  if (-not (ValidProposal $good)) { Write-Host 'FAIL: valid proposal rejected'; $ok = $false }
  if (ValidProposal $noVerify) { Write-Host 'FAIL: proposal without verify accepted'; $ok = $false }
  if (ValidProposal $notItem) { Write-Host 'FAIL: non-item accepted'; $ok = $false }
  if (-not (Test-Path $Backlog)) { Write-Host "FAIL: backlog missing at $Backlog"; $ok = $false }
  else {
    $n = OpenItems
    if ($n -lt 1) { Write-Host "FAIL: expected open backlog items, found $n"; $ok = $false }
    else { Write-Host "ok: $n open backlog items" }
  }
  if (-not (Test-Path 'ops/selfimprove-loop.ps1')) { Write-Host 'FAIL: selfimprove-loop.ps1 missing'; $ok = $false }
  if (-not (Test-Path $omni)) { Write-Host "FAIL: omni.ps1 missing at $omni"; $ok = $false }
  if (Test-Path $KillFile) { Write-Host "WARN: kill file $KillFile already exists -- the loop would exit immediately" }
  if ($ok) { Write-Host 'overnight selftest: OK'; exit 0 } else { exit 1 }
}

New-Item -ItemType Directory -Force -Path (Split-Path $Journal) | Out-Null
if (Test-Path $KillFile) { Remove-Item $KillFile -Force }

$start = Get-Date
$round = 0
Note 'start' "overnight loop up; kill with: New-Item $KillFile"
Journal "`n# Overnight run $(Get-Date -Format 'yyyy-MM-dd HH:mm')`n"

while ($true) {
  if (Test-Path $KillFile) { Note 'stop' 'kill file present -- exiting'; break }
  $elapsed = ((Get-Date) - $start).TotalHours
  if ($Hours -gt 0 -and $elapsed -ge $Hours) { Note 'stop' "wall-clock cap ${Hours}h reached"; break }

  $round++
  $report = AccuracyReport
  $fence = '```'
  Journal ("`n## Round $round -- $(Get-Date -Format 'HH:mm')`n`n$fence`n$report`n$fence`n")
  Note 'round' "round $round starting ($([math]::Round($elapsed,2))h elapsed)"

  if ((OpenItems) -eq 0) {
    if ((Refill) -eq 0) {
      Note 'idle' 'backlog empty and refill produced nothing -- sleeping 10m'
      Start-Sleep -Seconds 600
      continue
    }
  }

  $chunk = [math]::Max(0.05, $CycleMinutes / 60.0)
  if ($Hours -gt 0) { $chunk = [math]::Min($chunk, [math]::Max(0.05, $Hours - $elapsed)) }
  Note 'work' "handing ${CycleMinutes}m to selfimprove-loop"
  try {
    & powershell -NoProfile -File 'ops/selfimprove-loop.ps1' -Hours $chunk -CyclePauseSec 30 2>&1 |
      ForEach-Object { Write-Host "    $_" }
  } catch {
    Note 'work-error' "selfimprove-loop failed: $_"
    Start-Sleep -Seconds 120
  }
}

$final = AccuracyReport
$fence = '```'
Journal ("`n## Final -- $(Get-Date -Format 'yyyy-MM-dd HH:mm')`n`n$fence`n$final`n$fence`n")
Note 'done' "stopped after $round rounds"
Write-Host $final
