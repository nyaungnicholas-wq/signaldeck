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
  # IMPLEMENT runs on the SAME lane as PROPOSE (qwen3.6:27b), reversing the
  # earlier choice of `fast`. Both halves of that choice were measured wrong:
  #
  # 1. "qwen2.5-coder:7b answered comparable asks in 20-50 seconds" was timing a
  #    harness that could not pass. With verify fixed and a gate that demands a
  #    real measurement, 60 genuine draws produced zero executable scripts --
  #    SyntaxError, unclosed parens, undefined cursors. Fast wrong is not fast.
  # 2. "qwen3.6:27b took over ten minutes" did not reproduce: 79 seconds COLD,
  #    generating correct working sqlite3 code on the first draw (2026-08-02).
  #
  # Sharing one model across both stages also removes the eviction thrash that
  # made the 27B call fail and silently fall through to the PAID gateway -- the
  # loop was billing Mistral for PROPOSE while claiming zero spend.
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

# The measurement rules a CODE-GENERATION call actually needs. The full protocol
# governs judgment -- which hypotheses are worth testing and which results count
# as evidence -- and none of that changes a line of Python. Sending all 11KB of
# it alongside a 1.5KB code spec produced a 17-minute generation that timed out
# locally and then failed over to a gateway that also refused. Cycle 1 died there.
# PROPOSE and JUDGE still receive the protocol in full, because that is where it
# does its work.
$CODE_RULES = @"
THE ACTUAL SCHEMA. These are the real columns; there are no others. A generated
script that invented a column (predicted_class) died on the very first run,
because a model with no schema writes SQL against the database it imagines.

- bars(symbol_id, tf, ts, open, high, low, close, volume)  -- 13.4M rows
  tf is one of '1d', '1h', '1m'. ts is a unix epoch integer.
- symbols(id, symbol, market, name, active, added_at, stream, delisted_at)
  -- 1,080 rows. market is 'stocks' or 'crypto'.
- regime_outcomes(id, symbol_id, kind, ts, day, horizon_days, regime, conviction,
  historical_accuracy, rank, resolved_at, actual, correct, naive_label, revision,
  basis_epoch)  -- 20,787 rows. NOTE: correct and resolved_at are NULL on every
  row today, so this table cannot supply labels yet.
- prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at,
  basis_epoch)  -- 280,087 rows. up is the realised direction, prob the
  model's probability, fwd_return the realised forward return. This is the
  table with usable labels.
- scores(symbol_id, horizon, ts, score, components)  -- 1.55M rows.

Derive labels from bars closes or from prediction_outcomes.up / fwd_return.
Never reference a column not listed above.

Measurement rules this script must obey:
- Open data/signaldeck.db READ-ONLY. Never write to it.
- As-of discipline: every input must be computable at the decision timestamp.
  No value from a bar at or after the label window may inform a call.
- Hold out the most recent 20% as a sealed era and report it separately.
- Count independent observations, not rows: one (symbol, UTC day) is one
  observation, however many forecasts resolve on it.
- Report the base rate of the predicted class WITHIN the issued subset. A
  precision at or near that base rate is unskilled classification, not an edge.
- Never fabricate. If the data is insufficient, print INSUFFICIENT=1 and exit 0.
"@

# One worker call. $context defaults to the full protocol; the code stage passes
# the compact rules instead.
function Ask([string]$task, [string]$lane, [string]$outFile, [int]$timeoutSec = 900,
  [string]$context = $null, [string]$verify = '', [int]$samples = 1) {
  if (-not $context) { $context = $PROTOCOL }
  $prompt = @"
$context

=== END OF CONTEXT. YOUR TASK FOLLOWS. ===

$task
"@
  $tmp = Join-Path $env:TEMP "eighty-$([guid]::NewGuid().ToString('N').Substring(0,8)).txt"
  # Capture omni's own diagnostics instead of discarding them. Out-Null here
  # meant 44 consecutive cycles logged a bare "worker-empty" while omni was
  # printing the actual cause -- a local model timing out and falling through to
  # the gateway -- to a stream nobody read.
  $diag = ''
  # Prompt goes through a file, never the command line: PowerShell re-parses
  # `-File` arguments and any "-word" in the protocol text binds as a parameter.
  $pf = Join-Path $env:TEMP "eighty-prompt-$([guid]::NewGuid().ToString('N').Substring(0,8)).txt"
  Set-Content -Path $pf -Value $prompt -Encoding UTF8
  try {
    # NOT $args -- that is an automatic variable and assigning it inside a
    # function with a param block is a footgun that reads as working code.
    $omniArgs = @('-NoProfile', '-File', $omni, '-PromptFile', $pf, '-Task', $lane, '-TimeoutSec', $timeoutSec)
    if ($verify -ne '') {
      # omni.ps1 refuses -Verify without -Out ("it runs against the written
      # file") and refuses -Samples>1 without -Verify. Omitting -Out here made
      # every verified call exit 2 before reaching a model.
      $tmp = $outFile
      # -Attempts is omni's re-brief count (it feeds the failure back into the
      # next try); -Samples is independent draws within one attempt. 3x5 was
      # never actually exercised -- the verify command was broken until now --
      # so this is the first run where retrying can converge on anything.
      $omniArgs += '-Out', $tmp, '-Verify', $verify, '-Samples', $samples, '-Attempts', 6
    } else {
      $omniArgs += '-Out', $tmp
    }
    $diag = (& powershell $omniArgs 2>&1 | Out-String)
    $omniExit = $LASTEXITCODE
  } catch { Ev 'worker-error' @{ err = "$_" }; return $null }
  if ($diag -match 'via\s+(\S+)') { Ev 'served-by' @{ model = $Matches[1] } }

  if (-not (Test-Path $tmp) -or (Get-Item $tmp).Length -lt 10) {
    Ev 'worker-empty' @{ diag = (($diag -split "`n" | Where-Object { $_ -match '\S' } |
          Select-Object -Last 2) -join ' ') }
    return $null
  }

  # omni already stripped fences and BOM and PROVED the artifact runs, so the
  # post-processing below would only re-mangle a file that is already correct.
  #
  # EXCEPT when it proved the opposite. omni calls Write-Artifact BEFORE each
  # verify, so -Out exists and is non-empty even when every attempt FAILED and
  # omni exited 1. Sizing the file is therefore not evidence it runs -- the
  # first version of this branch logged 'implement-verified' over a script that
  # died on a NameError one second later. The exit code is the only honest
  # signal, so a failed verify returns null and the cycle dies as 'no-code'
  # rather than parading an unverified artifact as verified.
  if ($verify -ne '') {
    if ($omniExit -ne 0) {
      Ev 'verify-exhausted' @{ exit = $omniExit
        diag = (($diag -split "`n" | Where-Object { $_ -match '\S' } |
                 Select-Object -Last 6) -join ' | ') }
      return $null
    }
    Ev 'implement-verified' @{ bytes = (Get-Item $tmp).Length }
    return (Get-Content $tmp -Raw)
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
  # The verify command travels to omni.ps1 as a `powershell -File` ARGUMENT, and
  # -File re-parses arguments: quotes are stripped and the value is cut at the
  # first space, so `python "C:\...\Desktop\claude code\...\h0001.py"` arrived as
  # `python C:\Users\Nicholas_N\Desktop\claude` and every verify died with
  # "python: can't open file or directory" -- which omni then reported as a
  # failing artifact rather than a broken command. Same defect class as the
  # -PromptFile fix. A repo-RELATIVE path has no spaces, so it needs no quotes
  # and survives the hop intact; omni inherits this process's cwd, which
  # Set-Location pinned to $repo at startup.
  $scriptRel = "research\eighty\" + ("h{0:D4}.py" -f $cycle)
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
"@ $CodeLane $script 1200 $CODE_RULES -verify "python ops\verify_hypothesis.py $scriptRel" -samples 5

  if (-not $code) { Ev 'no-code'; Start-Sleep -Seconds $CyclePauseSec; continue }

  # --- 3. VERIFY: it must actually run -----------------------------------
  $r = Run "h$cycle" "python `"$script`"" 900
  if (-not $r.Ok) {
    $tail = ($r.Output -split "`n" | Where-Object { $_ -match '\S' } | Select-Object -Last 12) -join ' | '
    Ev 'script-failed' @{ cycle = $cycle; tail = $tail }
    Add-Content $journal "`n## Cycle $cycle - KILLED (script did not run)`n`n$hypothesis`n`n``````$(($r.Output -split "`n" | Select-Object -Last 6) -join "`n")```````n"
    Start-Sleep -Seconds $CyclePauseSec
    continue
  }
  Ev 'script-ran' @{ cycle = $cycle }

  # --- 3b. SANITY: kill structurally impossible output in code -----------
  # A script that RUNS is not a script that MEASURED. The first generated one
  # printed OPPORTUNITIES=-113750, PRECISION=0.0000 on 247,360 issued calls, and
  # EFFECTIVE_N identical to ISSUED -- a negative population, a precision no
  # rule could produce, and a design effect never applied. Numbers like that are
  # arithmetically impossible, not merely unpromising, and deciding that costs a
  # regex rather than a 27B judgment call. Do it here so the judge only ever
  # sees output that could be real.
  $out = $r.Output
  function Num([string]$key) {
    if ($out -match "(?m)^$key=(-?[\d.]+)\s*$") { return [double]$Matches[1] }
    return $null
  }
  $issued = Num 'ISSUED'; $opps = Num 'OPPORTUNITIES'; $prec = Num 'PRECISION'
  $effN = Num 'EFFECTIVE_N'; $days = Num 'DISTINCT_DAYS'
  $insane = @()
  if ($out -match '(?m)^INSUFFICIENT=1\s*$') { $insane += 'reported INSUFFICIENT' }
  if ($null -eq $issued -or $null -eq $prec) { $insane += 'did not print ISSUED/PRECISION' }
  if ($null -ne $opps -and $opps -lt 0) { $insane += "OPPORTUNITIES is negative ($opps)" }
  if ($null -ne $issued -and $null -ne $opps -and $opps -gt 0 -and $issued -gt $opps) {
    $insane += "ISSUED ($issued) exceeds OPPORTUNITIES ($opps)"
  }
  if ($null -ne $prec -and ($prec -lt 0 -or $prec -gt 1)) { $insane += "PRECISION out of [0,1] ($prec)" }
  if ($null -ne $issued -and $null -ne $effN -and $issued -gt 0 -and $effN -ge $issued) {
    $insane += 'EFFECTIVE_N is not below ISSUED, so no design effect was applied'
  }
  if ($null -ne $days -and $null -ne $issued -and $days -gt $issued) {
    $insane += "DISTINCT_DAYS ($days) exceeds ISSUED ($issued)"
  }
  if ($insane.Count -gt 0) {
    Ev 'insane-output' @{ cycle = $cycle; reasons = ($insane -join '; ') }
    Add-Content $journal @"

## Cycle $cycle - KILLED (output not arithmetically possible)  ($(Get-Date -Format 'yyyy-MM-dd HH:mm'))

$hypothesis

**Rejected before judging:** $($insane -join '; ')

``````
$(($out -split "`n" | Select-Object -Last 10) -join "`n")
``````
"@
    Start-Sleep -Seconds $CyclePauseSec
    continue
  }

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
