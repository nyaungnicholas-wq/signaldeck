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
# Pull repo-relative source paths out of a brief or a compiler error, keeping
# only ones that actually exist. This is how the worker learns WHICH file it is
# allowed to rewrite -- and how it gets to see that file at all.
function TargetFiles([string]$text, [string]$verifyCmd = '') {
  # The worker must NEVER be handed the file that implements the check it is
  # being asked to satisfy. Extraction on backlog item 1 returned
  # tools/research_liveness.py -- the liveness checker itself -- and a model told
  # "make this command exit zero" while holding the checker will eventually just
  # gut the checker. Anything named in the verify command is off limits, as is
  # anything that looks like a test.
  $banned = New-Object System.Collections.Generic.List[string]
  foreach ($m in [regex]::Matches($verifyCmd, '((?:[\w.-]+[/\\])*[\w.-]+\.(?:go|py|ts|tsx|ps1|sh|mjs))')) {
    $banned.Add(($m.Groups[1].Value -replace '\\', '/'))
  }

  $hits = [regex]::Matches($text, '(?<![\w./\\-])((?:[\w.-]+[/\\])+[\w.-]+\.(?:go|py|ts|tsx|ps1|sh|md|json|mjs))')
  $out = New-Object System.Collections.Generic.List[string]
  foreach ($m in $hits) {
    $p = $m.Groups[1].Value -replace '\\', '/'
    if ($p -match '^(data|logs|node_modules|\.git|quarantine)/') { continue }
    if ($p -match '(^|/)(test_[\w.-]+\.py|[\w.-]+_test\.go|[\w.-]+\.test\.tsx?)$') { continue }
    if ($banned -contains ($p -split '/')[-1] -or $banned -contains $p) { continue }
    if ((Test-Path (Join-Path $repo $p)) -and -not $out.Contains($p)) { $out.Add($p) }
  }
  $out
}

# Returns the list of files it changed and verified, or $null. The caller commits
# exactly those -- never `git add -A`, which in a tree shared with another agent
# sweeps up that agent's unreviewed work. That happened once already.
#
# WHOLE FILE IN, WHOLE FILE OUT. The first design asked for a unified diff and
# never showed the worker the code: 41 consecutive cycles produced diffs, 0 of
# which applied. A local model cannot invent matching context for a file it has
# never read, and a diff is unusable the moment one context line is wrong. So the
# file goes in via -File and the complete rewritten file comes back via -Out.
function BriefWorker([string]$task, [string]$evidence, [string]$verifyCmd, [string]$lane = 'code', [string]$explicitFile = '') {
  if (-not (Test-Path $omni)) { Note 'omni-missing'; return $null }

  if ($explicitFile -and (Test-Path (Join-Path $repo $explicitFile))) {
    $targets = @($explicitFile)
  }
  else {
    $targets = TargetFiles "$task`n$evidence" $verifyCmd
  }
  if ($targets.Count -eq 0) { Note 'no-target-file'; return $null }
  $target = $targets[0]
  $full = Join-Path $repo $target
  Note 'worker-start' @{ file = $target; lane = $lane; candidates = $targets.Count }

  $prompt = @"
You are fixing ONE file in the SignalDeck repository. You have no tools and no
memory: this brief and the attached file are everything you get.

TASK
$task

EVIDENCE (verbatim output of the check that must pass)
$evidence

THE FILE TO FIX: $target
Its complete current contents are attached below.

RULES THAT ARE NOT NEGOTIABLE
- Fix the ROOT CAUSE. Do not weaken, skip, delete or special-case the check that
  caught this. This repository's checks exist to refuse dishonest results; a
  green light obtained by softening a check is a regression, not a fix.
- Change as little as possible. Keep every unrelated line byte-identical.
- Match the surrounding style. Add no dependencies.

OUTPUT FORMAT -- follow exactly:
Return the COMPLETE contents of $target after your fix. Start at the very first
line of the file and end at the very last. No markdown fences, no ``` markers,
no commentary, no diff, no ellipses, no "rest of file unchanged". The raw file
and nothing else. Your output will be written directly over $target.

It is accepted only if this command then exits zero:
  $verifyCmd
"@

  $outFile = Join-Path $env:TEMP "sd-worker-$([guid]::NewGuid().ToString('N').Substring(0,8)).out"
  try {
    & powershell -NoProfile -File $omni -Prompt $prompt -File $full -Task $lane -Out $outFile -TimeoutSec 900 2>&1 | Out-Null
  } catch { Note 'worker-error' @{ err = "$_" }; return $null }
  if (-not (Test-Path $outFile) -or (Get-Item $outFile).Length -lt 20) { Note 'worker-empty'; return $null }

  # omni.ps1 writes a UTF-8 BOM; models wrap output in ``` despite instructions.
  $bytes = [IO.File]::ReadAllBytes($outFile)
  if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
    $bytes = $bytes[3..($bytes.Length - 1)]
  }
  $new = ([Text.Encoding]::UTF8.GetString($bytes) -replace "`r`n", "`n")
  $new = (($new -split "`n") | Where-Object { $_ -notmatch '^\s*```' }) -join "`n"
  $new = $new.TrimEnd() + "`n"

  # Truncation is the failure mode that matters: a model that stops early hands
  # back a file that still parses but has lost its tail. Anything under 60% of
  # the original is refused rather than written.
  $old = ([IO.File]::ReadAllText($full) -replace "`r`n", "`n")
  if ($new.Length -lt ($old.Length * 0.6)) {
    Note 'output-truncated' @{ file = $target; oldLen = $old.Length; newLen = $new.Length }
    return $null
  }
  if ($new -eq $old) { Note 'no-change-produced' @{ file = $target }; return $null }

  [IO.File]::WriteAllText($full, $new, (New-Object Text.UTF8Encoding $false))

  $v = Run 'verify' $verifyCmd 1800
  if (-not $v.Ok) {
    Note 'verify-failed' @{ file = $target; tail = ($v.Output -split "`n" | Select-Object -Last 3) -join ' ' }
    git checkout -- $target 2>&1 | Out-Null   # a fix that does not verify is not a fix
    return $null
  }
  Note 'verify-passed' @{ file = $target }
  return @($target)
}

function NextBacklogItem {
  if (-not (Test-Path $backlog)) { return $null }
  $text = Get-Content $backlog -Raw
  $blocks = [regex]::Matches($text, '(?ms)^## \[ \] (.+?)$(.*?)(?=^## |\z)')
  foreach ($b in $blocks) {
    $body = $b.Groups[2].Value
    $m = [regex]::Match($body, '(?m)^verify: `(.+)`\s*$')
    if ($m.Success) {
      $fm = [regex]::Match($body, '(?m)^files: `(.+)`\s*$')
      return [pscustomobject]@{
        Title  = $b.Groups[1].Value.Trim()
        Body   = $body.Trim()
        Verify = $m.Groups[1].Value.Trim()
        # An explicit target beats extraction, which guessed the liveness CHECKER
        # for item 1 -- the one file a worker must never be allowed to rewrite.
        File   = $(if ($fm.Success) { $fm.Groups[1].Value.Trim() } else { '' })
      }
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
      $worked = BriefWorker $item.Body '(no failing gate -- this is planned improvement work)' $item.Verify $BacklogLane $item.File
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
