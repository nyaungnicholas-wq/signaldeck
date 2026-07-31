<#
.SYNOPSIS
  Runs SignalDeck's verification battery on a loop for ~24h, hands every failure
  to an OmniRoute worker to fix, and commits what verifies.

.DESCRIPTION
  Claude is not in this loop. Local router tokens are free, Anthropic tokens are
  not, so the workers do the typing and a runnable check decides whether the work
  was any good. Nothing is accepted because a model said it was done -- every
  change is gated on the same command that failed before it.

  Two modes per cycle:
    RED   -- a gate failed. Brief a worker on that gate's output. Highest priority.
    GREEN -- everything passes. Take the first unchecked item from
            ops/IMPROVE_BACKLOG.md and work that instead.

  "No matter what scenario": every step is wrapped. A crashed gate, a dead
  router, a git conflict, a hung worker -- the cycle logs it and the next one
  starts anyway. The loop only ends when its deadline passes or you kill it.

.PARAMETER Hours
  Wall-clock hours to keep looping. Default 24.

.PARAMETER Push
  Push the working branch after each green commit. Off by default.

.PARAMETER AllowChainWrites
  Permit the loop to run the daemon against the live DB and let the registrar
  append prereg records. The DB is snapshotted before the first such cycle.
  Irreversible: the chain is append-only and a dirty build writes ungradable
  rows. Off by default.

.EXAMPLE
  pwsh -File ops/selfimprove-loop.ps1 -Hours 24 -Push
#>
[CmdletBinding()]
param(
  # Wall-clock cap. 0 means no cap -- run until the goal is met or you kill it.
  [double]$Hours = 24,
  # Stop as soon as the goal is met: every gate green AND no unchecked backlog
  # item left. Without this the loop keeps cycling until the clock runs out.
  [switch]$UntilGoal,
  [switch]$Push,
  [switch]$AllowChainWrites,
  [int]$CyclePauseSec = 60,
  # Model lanes. Both default to LOCAL Ollama models so a long run costs nothing:
  # code -> qwen3.6:27b, reason -> deepseek-r1:32b. 'best' would invert to the
  # paid gateway first, which is the wrong trade for a run measured in days.
  [string]$GateLane = 'code',
  [string]$BacklogLane = 'reason',
  [switch]$SelfTestOnly
)

$ErrorActionPreference = 'Continue'
$repo = Split-Path $PSScriptRoot -Parent
Set-Location $repo

$noTimeLimit = ($Hours -le 0)
$deadline = if ($noTimeLimit) { [datetime]::MaxValue } else { (Get-Date).AddHours($Hours) }
$journal  = Join-Path $repo 'logs\selfimprove.jsonl'
$omni     = Join-Path $env:USERPROFILE '.claude\scripts\omni.ps1'
$backlog  = Join-Path $repo 'ops\IMPROVE_BACKLOG.md'
$bash     = 'C:\Program Files\Git\bin\bash.exe'
New-Item -ItemType Directory -Force (Split-Path $journal) | Out-Null

function Note([string]$event, [hashtable]$data = @{}) {
  $rec = @{ ts = (Get-Date).ToString('o'); event = $event } + $data
  try { ($rec | ConvertTo-Json -Compress -Depth 6) | Add-Content $journal } catch { }
  Write-Host "[$((Get-Date).ToString('HH:mm:ss'))] $event $($data | ConvertTo-Json -Compress -Depth 3)"
}

# Run a command, capture combined output, never throw. The exit code is the
# verdict -- a gate that crashes is a gate that failed, not a gate to skip.
#
# The exit code travels in a sentinel line, NOT in the job state: `exit 3` inside
# a background job leaves State=Completed, so reading job state would have marked
# every gate green forever and the loop would have "improved" a repo it never
# actually tested. The sentinel is the only honest channel here.
function Run([string]$name, [string]$body, [int]$timeoutSec = 1800) {
  $out = ''; $code = 1
  $wrapped = [scriptblock]::Create(@"
param(`$repo)
Set-Location `$repo
`$global:LASTEXITCODE = 0
try { $body } catch { Write-Output "EXCEPTION: `$_"; `$global:LASTEXITCODE = 1 }
Write-Output "__EXIT__:`$(if (`$null -eq `$global:LASTEXITCODE) { 0 } else { `$global:LASTEXITCODE })"
"@)
  try {
    $job = Start-Job -ScriptBlock $wrapped -ArgumentList $repo
    if (Wait-Job $job -Timeout $timeoutSec) {
      $out = (Receive-Job $job 2>&1 | Out-String)
      if ($out -match '__EXIT__:(\d+)') { $code = [int]$Matches[1] }
      else { $code = 1 }   # no sentinel means it died before finishing
    }
    else { Stop-Job $job -ErrorAction SilentlyContinue; $out = "TIMEOUT after ${timeoutSec}s"; $code = 1 }
    Remove-Job $job -Force -ErrorAction SilentlyContinue
  } catch { $out = "$_"; $code = 1 }
  [pscustomobject]@{ Name = $name; Ok = ($code -eq 0); Output = ($out -replace '__EXIT__:\d+', '') }
}

# --- the gates -------------------------------------------------------------
# NEVER call `exit` in a gate body. Run() reports the verdict through a sentinel
# line printed AFTER the body, and `exit` terminates the job before that line is
# written -- Run() then sees no sentinel and calls the gate RED. Every gate read
# red on the first live run because of exactly this. Leave $LASTEXITCODE set by
# the last native command and let Run() read it.
#
# Kept as its own function so the test can exercise the REAL bodies. When the
# test used stand-in commands instead, it passed while all seven gates were
# broken.
function GateSpecs {
  @(
    @{ n = 'go-build';   c = 'cd daemon; go build ./... 2>&1' },
    @{ n = 'go-vet';     c = 'cd daemon; go vet ./... 2>&1' },
    @{ n = 'go-test';    c = 'cd daemon; go test ./... 2>&1' },
    @{ n = 'py-tests';   c = 'cd tools; $f=0; foreach($m in "test_accuracy_registry","test_audit_register","test_deployment_drift","test_schema_contract_check","test_research_liveness"){ python -m unittest $m 2>&1; if($LASTEXITCODE -ne 0){$f=1} }; $global:LASTEXITCODE = $f' },
    @{ n = 'web-types';  c = 'cd web; npx tsc --noEmit 2>&1' },
    @{ n = 'web-lint';   c = 'cd web; npx eslint . --max-warnings 0 2>&1' },
    @{ n = 'web-build';  c = 'cd web; npx next build 2>&1' }
  )
}

# Each returns $true when it passes. Output is what the worker gets briefed on,
# so it must be the real compiler/test text, not a summary.
function Gates {
  $g = @()
  foreach ($spec in (GateSpecs)) {
    $r = Run $spec.n $spec.c
    $g += $r
    if (-not $r.Ok) { Note 'gate-red' @{ gate = $spec.n } }
  }
  # The publish gate is bash, and it is the one that knows about secrets.
  if (Test-Path $bash) {
    $posix = ($repo -replace '\\', '/' -replace '^([A-Za-z]):', '/$1').ToLower()
    $r = Run 'publish-scan' "& '$bash' -lc ""cd '$posix' && bash ops/pre-publish-scan.sh""" 900
    $g += $r
    if (-not $r.Ok) { Note 'gate-red' @{ gate = 'publish-scan' } }
  }
  $g
}

# --- worker briefing -------------------------------------------------------
# Vague in, slop out. Name the gate, paste its real output, state the rule.
# Returns the list of files it changed and verified, or $null. The caller commits
# exactly those -- never `git add -A`, which in a tree shared with another agent
# sweeps up that agent's unreviewed work. That happened once already.
function BriefWorker([string]$task, [string]$evidence, [string]$verifyCmd, [string]$lane = 'code') {
  if (-not (Test-Path $omni)) { Note 'omni-missing'; return $null }
  $prompt = @"
You are fixing the SignalDeck repository at $repo. You are a worker with no
tools and no memory: this brief is everything you get.

TASK
$task

FAILING EVIDENCE (verbatim output of the check that must pass)
$evidence

RULES THAT ARE NOT NEGOTIABLE
- Fix the ROOT CAUSE. Do not weaken, skip, delete or special-case the check that
  caught this. This repository's checks exist to refuse dishonest results; a
  green light obtained by softening a check is a regression, not a fix.
- Do not touch anything under data/, .env, or the prereg chain.
- Smallest correct diff. Match the surrounding style. No new dependencies.
- Output ONLY a unified diff applicable with `git apply`. No prose.

The change is accepted only if this command then exits zero:
  $verifyCmd
"@
  $patch = Join-Path $env:TEMP "sd-worker-$([guid]::NewGuid().ToString('N').Substring(0,8)).patch"
  try {
    & powershell -NoProfile -File $omni -Prompt $prompt -Task $lane -Out $patch -TimeoutSec 900 2>&1 | Out-Null
  } catch { Note 'worker-error' @{ err = "$_" }; return $null }
  if (-not (Test-Path $patch) -or (Get-Item $patch).Length -lt 20) { Note 'worker-empty'; return $null }

  # Revert ONLY what this patch touched. `git checkout -- .` would also wipe any
  # uncommitted edit made by anything else sharing this working tree, and this
  # repo is edited by more than one agent.
  $touched = @(git apply --numstat $patch 2>$null | ForEach-Object { ($_ -split "`t")[2] } | Where-Object { $_ })
  function RevertTouched {
    if ($touched.Count -eq 0) { Note 'revert-skipped-unknown-files'; return }
    foreach ($f in $touched) { git checkout -- $f 2>&1 | Out-Null }
    Note 'reverted' @{ files = ($touched -join ',') }
  }

  if ($touched.Count -eq 0) { Note 'patch-touches-nothing'; return $null }

  git apply --3way $patch 2>&1 | Out-Null
  if ($LASTEXITCODE -ne 0) { Note 'patch-rejected'; RevertTouched; return $null }

  $v = Run 'verify' $verifyCmd 1800
  if (-not $v.Ok) {
    Note 'verify-failed' @{ tail = ($v.Output -split "`n" | Select-Object -Last 3) -join ' ' }
    RevertTouched   # a fix that does not verify is not a fix
    return $null
  }
  Note 'verify-passed' @{ files = ($touched -join ',') }
  return $touched
}

function NextBacklogItem {
  if (-not (Test-Path $backlog)) { return $null }
  $text = Get-Content $backlog -Raw
  $blocks = [regex]::Matches($text, '(?ms)^## \[ \] (.+?)$(.*?)(?=^## |\z)')
  foreach ($b in $blocks) {
    $body = $b.Groups[2].Value
    $m = [regex]::Match($body, '(?m)^verify: `(.+)`\s*$')
    if ($m.Success) {
      return [pscustomobject]@{ Title = $b.Groups[1].Value.Trim(); Body = $body.Trim(); Verify = $m.Groups[1].Value.Trim() }
    }
    Note 'backlog-item-unverifiable' @{ title = $b.Groups[1].Value.Trim() }
  }
  $null
}

# Dot-source with -SelfTestOnly to get the functions without starting the loop.
# ops/test-selfimprove-loop.ps1 tests the REAL Run(), not a copy of it -- a test
# that duplicates the code it checks passes happily while the original rots.
if ($SelfTestOnly) { return }

# --- start -----------------------------------------------------------------
$branch = (git rev-parse --abbrev-ref HEAD).Trim()
if ($branch -in @('main', 'master')) {
  $branch = "selfimprove/$(Get-Date -Format 'yyyyMMdd-HHmm')"
  git checkout -b $branch 2>&1 | Out-Null
}
Note 'loop-start' @{
  branch      = $branch
  deadline    = $(if ($noTimeLimit) { 'none' } else { $deadline.ToString('o') })
  untilGoal   = [bool]$UntilGoal
  push        = [bool]$Push
  chainWrites = [bool]$AllowChainWrites
  gateLane    = $GateLane
  backlogLane = $BacklogLane
}

if ($AllowChainWrites) {
  $snap = Join-Path $repo "data\signaldeck.db.loopsnapshot"
  if (-not (Test-Path $snap)) {
    try { Copy-Item (Join-Path $repo 'data\signaldeck.db') $snap -Force; Note 'db-snapshot' @{ path = $snap } }
    catch { Note 'db-snapshot-failed' @{ err = "$_" } }
  }
}

$cycle = 0
while ((Get-Date) -lt $deadline) {
  $cycle++
  $remaining = if ($noTimeLimit) { 'unlimited' } else { [math]::Round(($deadline - (Get-Date)).TotalHours, 2) }
  Note 'cycle-start' @{ cycle = $cycle; remainingH = $remaining }

  try { git pull --rebase --autostash 2>&1 | Out-Null } catch { Note 'pull-failed' @{ err = "$_" } }

  $results = Gates
  $red = @($results | Where-Object { -not $_.Ok })
  Note 'gates' @{ total = $results.Count; red = $red.Count; failing = ($red.Name -join ',') }

  # THE GOAL: every gate green and nothing left unchecked in the backlog. Checked
  # against freshly-run gates, never against a cached verdict -- "we were green
  # last cycle" is not evidence that we are green now.
  if ($UntilGoal -and $red.Count -eq 0 -and $null -eq (NextBacklogItem)) {
    Note 'GOAL-MET' @{ cycle = $cycle; gates = $results.Count }
    break
  }

  $worked = $false
  if ($red.Count -gt 0) {
    $g = $red[0]
    $cmd = switch ($g.Name) {
      'go-build'     { 'cd daemon; go build ./...' }
      'go-vet'       { 'cd daemon; go vet ./...' }
      'go-test'      { 'cd daemon; go test ./...' }
      'py-tests'     { 'cd tools; python -m unittest test_accuracy_registry test_audit_register test_deployment_drift test_schema_contract_check test_research_liveness' }
      'web-types'    { 'cd web; npx tsc --noEmit' }
      'web-lint'     { 'cd web; npx eslint . --max-warnings 0' }
      'web-build'    { 'cd web; npx next build' }
      'publish-scan' { "& '$bash' -lc ""cd '/c/Users/Nicholas_N/Desktop/claude code/signaldeck' && bash ops/pre-publish-scan.sh""" }
      default        { 'exit 1' }
    }
    $tail = ($g.Output -split "`n" | Select-Object -Last 80) -join "`n"
    $worked = BriefWorker "The '$($g.Name)' gate is failing. Make it pass." $tail $cmd $GateLane
  }
  else {
    $item = NextBacklogItem
    if ($null -eq $item) { Note 'backlog-empty' }
    else {
      Note 'backlog-item' @{ title = $item.Title }
      $worked = BriefWorker $item.Body '(no failing gate -- this is planned improvement work)' $item.Verify $BacklogLane
      if ($worked) {
        # Tick it off only after its own verification passed.
        (Get-Content $backlog -Raw).Replace("## [ ] $($item.Title)", "## [x] $($item.Title)") |
          Set-Content $backlog -NoNewline
      }
    }
  }

  if ($worked -and $worked.Count -gt 0) {
    # Re-run every gate before committing: a fix that repairs its own check and
    # breaks another one is a net loss, and only the full battery can see that.
    $after = Gates
    if (@($after | Where-Object { -not $_.Ok }).Count -eq 0) {
      foreach ($f in $worked) { git add -- $f 2>&1 | Out-Null }
      git commit -q -m "selfimprove cycle ${cycle}: $($worked -join ', ')" 2>&1 | Out-Null
      Note 'committed' @{ cycle = $cycle; files = ($worked -join ',') }
      if ($Push) {
        git push -u origin $branch 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { Note 'pushed' } else { Note 'push-failed' }
      }
    }
    else {
      Note 'regression-reverted' @{ broke = (@($after | Where-Object { -not $_.Ok }).Name -join ',') }
      foreach ($f in $worked) { git checkout -- $f 2>&1 | Out-Null }
    }
  }

  Start-Sleep -Seconds $CyclePauseSec
}

Note 'loop-end' @{ cycles = $cycle }
