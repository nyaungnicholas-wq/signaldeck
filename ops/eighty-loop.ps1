<#
.SYNOPSIS
  Drives EIGHTY_PERCENT_SUPERPROMPT.md through OmniRoute, indefinitely, with
  ZERO Claude involvement.

.DESCRIPTION
  Claude's entire role is to have written this file and the protocol it carries.
  At runtime there are no Claude calls -- not one -- so the loop's Claude token
  consumption is 0 by construction rather than by a budget that has to be
  policed. That is the only honest way to enforce "Claude barely touches this":
  a local script cannot meter a remote model it never invokes, so it must simply
  never invoke it.

  Every cycle is one hypothesis, run end to end by local workers:

    1. PROPOSE  - a mechanism and an abstention rule, stated BEFORE any data is
                  touched (protocol section 3, steps 1-2).
    2. IMPLEMENT- a self-contained Python script that measures precision, the
                  issued-subset base rate, abstention rate and effective N.
    3. VERIFY   - the script must RUN. Output that does not execute is discarded;
                  a hypothesis is not evidence until a command produced its numbers.
    4. JUDGE    - the protocol's nine acceptance criteria are applied to the real
                  output. Almost everything is killed here, which is the loop
                  working rather than failing.
    5. RECORD   - kept or killed, the result is appended to the research journal
                  with its numbers. A search whose judgments cannot be counted is
                  an unverifiable null result.

  The protocol is passed VERBATIM as standing context on every worker call, so
  the traps in section 0 and the prohibitions in section 5 are in front of the
  model at all times rather than summarised away.

.PARAMETER Hours
  Wall-clock cap. 0 = run until stopped.

.PARAMETER MaxCycles
  Hard iteration cap, a runaway backstop rather than a target.

.EXAMPLE
  pwsh -File ops/eighty-loop.ps1 -Hours 24
#>
[CmdletBinding()]
param(
  [double]$Hours = 0,
  [int]$MaxCycles = 500,
  # BOTH stages run on the `code` lane (qwen3.6:27b) deliberately.
  #
  # The reason lane (deepseek-r1:32b, 19GB) TIMED OUT after 610 seconds against
  # a 11.4KB prompt while qwen3.6:27b (18GB) was already resident -- two models
  # that size cannot coexist, so every lane switch evicts and reloads, and under
  # that contention the call dies. omni then fell through to the PAID gateway
  # (mistral-large), which is both the wrong cost profile for a loop meant to run
  # for days and the reason no local token movement was visible.
  #
  # qwen3.6:27b stays resident, handled a 29KB prompt in 78-122s in testing, and
  # is a perfectly good reasoning model for this. One model, no thrash, no spend.
  [string]$Lane = 'code',
  [string]$CodeLane = 'code',
  [int]$CyclePauseSec = 30
)

$ErrorActionPreference = 'Continue'
$repo = Split-Path $PSScriptRoot -Parent
Set-Location $repo

# Reuse the harness that is already tested rather than writing a second one:
# Run() reports exit codes honestly through a sentinel, which is the single
# property this loop cannot do without.
. "$PSScriptRoot\selfimprove-loop.ps1" -SelfTestOnly

$protocolPath = Join-Path $repo 'EIGHTY_PERCENT_SUPERPROMPT.md'
if (-not (Test-Path $protocolPath)) { throw "protocol missing: $protocolPath" }
$PROTOCOL = Get-Content $protocolPath -Raw

$omni     = Join-Path $env:USERPROFILE '.claude\scripts\omni.ps1'
$journal  = Join-Path $repo 'logs\eighty-research.md'
$events   = Join-Path $repo 'logs\eighty-events.jsonl'
$work     = Join-Path $repo 'research\eighty'
New-Item -ItemType Directory -Force (Split-Path $journal) | Out-Null
New-Item -ItemType Directory -Force $work | Out-Null

$deadline = if ($Hours -le 0) { [datetime]::MaxValue } else { (Get-Date).AddHours($Hours) }

function Ev([string]$event, [hashtable]$data = @{}) {
  $rec = @{ ts = (Get-Date).ToString('o'); event = $event } + $data
  try { ($rec | ConvertTo-Json -Compress -Depth 6) | Add-Content $events } catch { }
  Write-Host "[$((Get-Date).ToString('HH:mm:ss'))] $event $($data | ConvertTo-Json -Compress -Depth 3)"
}

# One worker call. The protocol rides along on every single one.
function Ask([string]$task, [string]$lane, [string]$outFile, [int]$timeoutSec = 900) {
  $prompt = @"
$PROTOCOL

=== END OF PROTOCOL. YOUR TASK FOLLOWS. ===

$task
"@
  $tmp = Join-Path $env:TEMP "eighty-$([guid]::NewGuid().ToString('N').Substring(0,8)).txt"
  # Capture omni's own diagnostics instead of discarding them. Out-Null here
  # meant 44 consecutive cycles logged a bare "worker-empty" while omni was
  # printing the actual cause -- a local model timing out and falling through to
  # the gateway -- to a stream nobody read.
  $diag = ''
  try {
    $diag = (& powershell -NoProfile -File $omni -Prompt $prompt -Task $lane -Out $tmp `
        -TimeoutSec $timeoutSec 2>&1 | Out-String)
  } catch { Ev 'worker-error' @{ err = "$_" }; return $null }
  if ($diag -match 'via\s+(\S+)') { Ev 'served-by' @{ model = $Matches[1] } }

  if (-not (Test-Path $tmp) -or (Get-Item $tmp).Length -lt 10) {
    Ev 'worker-empty' @{ diag = (($diag -split "`n" | Where-Object { $_ -match '\S' } |
          Select-Object -Last 2) -join ' ') }
    return $null
  }

  # omni writes a UTF-8 BOM and models fence their output despite instructions.
  $bytes = [IO.File]::ReadAllBytes($tmp)
  if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
    $bytes = $bytes[3..($bytes.Length - 1)]
  }
  $text = ([Text.Encoding]::UTF8.GetString($bytes) -replace "`r`n", "`n")
  $text = (($text -split "`n") | Where-Object { $_ -notmatch '^\s*```' }) -join "`n"
  Remove-Item $tmp -ErrorAction SilentlyContinue
  if ($outFile) { [IO.File]::WriteAllText($outFile, $text, (New-Object Text.UTF8Encoding $false)) }
  return $text
}

Ev 'loop-start' @{ protocol = 'EIGHTY_PERCENT_SUPERPROMPT.md'; lane = $Lane; codeLane = $CodeLane
                   deadline = $(if ($Hours -le 0) { 'none' } else { $deadline.ToString('o') })
                   claudeCalls = 0 }

if (-not (Test-Path $journal)) {
  @"
# Eighty-percent research journal

Every hypothesis this loop has tested, kept or killed, with the numbers that
decided it. Written by local workers via OmniRoute; no Claude call participates
in this loop.

A killed hypothesis is evidence. The expected outcome of a disciplined search is
that almost everything here is killed.

"@ | Set-Content $journal
}

$cycle = 0
while ((Get-Date) -lt $deadline -and $cycle -lt $MaxCycles) {
  $cycle++
  Ev 'cycle-start' @{ cycle = $cycle }

  # --- 1. PROPOSE ---------------------------------------------------------
  $prior = if (Test-Path $journal) {
    (Get-Content $journal -Raw) -split "`n" | Select-Object -Last 120 | Out-String
  } else { '(none yet)' }

  $hypothesis = Ask @"
Propose ONE new hypothesis, in at most 12 lines.

Required, in this order:
  MECHANISM:  one sentence naming an economic reason this should work (a
              liquidity constraint, a flow imbalance, a participant behaviour, a
              slow-diffusing information source). A pattern with no mechanism is
              a coincidence you have not caught yet.
  HORIZON:    the prediction horizon.
  UNIVERSE:   which symbols, defined without hindsight.
  ENTRY:      the exact condition under which a call is ISSUED.
  ABSTAIN:    the exact condition under which no call is made. State this now,
              before any data is touched. Abstention is what buys precision.
  CLAIM:      the precision band you are committing to in advance.

Do NOT repeat a hypothesis already present in this journal tail:
$prior

Output the six labelled lines and nothing else.
"@ $Lane $null 900

  if (-not $hypothesis) { Ev 'no-hypothesis'; Start-Sleep -Seconds $CyclePauseSec; continue }
  Ev 'hypothesis' @{ cycle = $cycle; head = (($hypothesis -split "`n")[0]) }

  # --- 2. IMPLEMENT -------------------------------------------------------
  $script = Join-Path $work ("h{0:D4}.py" -f $cycle)
  $code = Ask @"
Write a SELF-CONTAINED Python 3 script that tests exactly this hypothesis:

$hypothesis

Hard requirements:
- Read ONLY from the read-only SQLite database at data/signaldeck.db, opened as
  sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True). Never write to it.
- Relevant tables: bars(symbol_id, tf, ts, open, high, low, close, volume),
  symbols(id, symbol, market), regime_outcomes, prediction_outcomes, scores.
- Respect as-of discipline: every input must be computable at the decision
  timestamp. No value from a bar at or after the label window may inform a call.
- Hold out the most recent 20% of the sample as a sealed era and report it
  separately from the rest.
- PRINT exactly these lines at the end, one per line, with real computed numbers:
    ISSUED=<count of calls issued>
    OPPORTUNITIES=<count of decision points considered>
    PRECISION=<hits/issued as a decimal>
    BASE_RATE=<base rate of the predicted class WITHIN the issued subset>
    DISTINCT_DAYS=<distinct UTC days on which a call was issued>
    EFFECTIVE_N=<issued count divided by the measured design effect>
    SEALED_PRECISION=<precision on the sealed era>
- If there is insufficient data, print INSUFFICIENT=1 and exit 0. Never fabricate.
- Standard library plus sqlite3 only. No pandas, no numpy, no network.
- Must run to completion in under 10 minutes.

Output the raw Python file only. No markdown fences, no commentary.
"@ $CodeLane $script 1200

  if (-not $code) { Ev 'no-code'; Start-Sleep -Seconds $CyclePauseSec; continue }

  # --- 3. VERIFY: it must actually run -----------------------------------
  $r = Run "h$cycle" "python `"$script`"" 900
  if (-not $r.Ok) {
    Ev 'script-failed' @{ cycle = $cycle; tail = (($r.Output -split "`n" | Select-Object -Last 2) -join ' ') }
    Add-Content $journal "`n## Cycle $cycle - KILLED (script did not run)`n`n$hypothesis`n`n``````$(($r.Output -split "`n" | Select-Object -Last 6) -join "`n")```````n"
    Start-Sleep -Seconds $CyclePauseSec
    continue
  }
  Ev 'script-ran' @{ cycle = $cycle }

  # --- 4. JUDGE against the protocol's criteria --------------------------
  $verdict = Ask @"
Here is a hypothesis and the REAL measured output of the script that tested it.

HYPOTHESIS:
$hypothesis

MEASURED OUTPUT:
$($r.Output -split "`n" | Select-Object -Last 40 | Out-String)

Apply the protocol's acceptance criteria to these numbers ONLY. Do not assume
any number that is not printed above; if something needed is missing, that is a
KILL for insufficient evidence.

Answer in at most 10 lines:
  VERDICT: KEEP or KILL
  REASON: one sentence citing the specific number that decided it
  PRECISION / BASE_RATE / EDGE: the three figures, or 'not reported'
  CRITERIA_MET: how many of the nine, listing which failed

Remember: precision alone is never evidence. A precision at or near the issued
subset base rate is unskilled classification and must be KILLED however high it
looks.
"@ $Lane $null 900

  if (-not $verdict) { $verdict = '(judge produced nothing - treated as KILL)' }
  $kept = $verdict -match 'VERDICT:\s*KEEP'
  Ev $(if ($kept) { 'KEPT' } else { 'killed' }) @{ cycle = $cycle }

  # --- 5. RECORD, kept or killed -----------------------------------------
  Add-Content $journal @"

## Cycle $cycle - $(if ($kept) { 'KEPT' } else { 'KILLED' })  ($(Get-Date -Format 'yyyy-MM-dd HH:mm'))

$hypothesis

**Measured**
``````
$(($r.Output -split "`n" | Select-Object -Last 12) -join "`n")
``````

**Verdict**
$verdict

Script: ``research/eighty/$(Split-Path $script -Leaf)``
"@

  Start-Sleep -Seconds $CyclePauseSec
}

Ev 'loop-end' @{ cycles = $cycle; claudeCalls = 0 }
