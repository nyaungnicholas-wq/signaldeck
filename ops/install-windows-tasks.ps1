<#
Recreate the launchd schedule as Windows Scheduled Tasks.

The repo's automation is defined by ops/com.*.plist. On the 2026-07-31 move to
Windows only ONE of those 14 jobs was recreated (the daemon), so the nightly
backup, accuracy grading, cleanup, restore rehearsal, research liveness and
bias regression had not run since. This reads the plists — the same files the
Mac uses, so the two schedules cannot drift — and registers the equivalents.

DEFAULT IS A REPORT. Nothing is registered unless -Install is passed.

  .\install-windows-tasks.ps1            # show what would happen
  .\install-windows-tasks.ps1 -Install   # register / update
  .\install-windows-tasks.ps1 -Remove    # unregister the managed tasks
#>
[CmdletBinding()]
param([switch]$Install, [switch]$Remove)

$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent

# Git Bash, never WSL. WSL has its own filesystem and cannot see C:\Users\...
# the way these scripts expect — the same defect that broke test_accuracy_registry.
$bash = @(
  (Join-Path $env:ProgramFiles 'Git\bin\bash.exe'),
  (Join-Path ${env:ProgramFiles(x86)} 'Git\bin\bash.exe')
) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $bash) { $bash = (Get-Command bash -ErrorAction SilentlyContinue).Source }
if (-not $bash) { Write-Output 'FATAL: no bash found; install Git for Windows.'; exit 1 }

# C:\path\to\repo -> /c/path/to/repo
$repoPosix = '/' + $repo.Substring(0, 1).ToLower() + ($repo.Substring(2) -replace '\\', '/')

function Get-PlistDict {
  # Apple plists are <key> followed by its value sibling. Flatten one dict.
  param($DictNode)
  $out = @{}
  $kids = @($DictNode.ChildNodes | Where-Object { $_.NodeType -eq 'Element' })
  for ($i = 0; $i -lt $kids.Count; $i += 2) {
    $k = $kids[$i]; $v = $kids[$i + 1]
    if ($k.Name -ne 'key' -or $null -eq $v) { continue }
    $out[$k.InnerText] = $v
  }
  return $out
}

function ConvertTo-Value {
  param($Node)
  switch ($Node.Name) {
    'string'  { $Node.InnerText }
    'integer' { [int]$Node.InnerText }
    'true'    { $true }
    'false'   { $false }
    'array'   { , @($Node.ChildNodes | Where-Object { $_.NodeType -eq 'Element' } | ForEach-Object { ConvertTo-Value $_ }) }
    'dict'    { Get-PlistDict $Node }
    default   { $Node.InnerText }
  }
}

function Get-TaskName {
  param([string]$Label)
  $leaf = ($Label -split '\.')[-1]
  $name = ($leaf -split '-' | ForEach-Object {
      if ($_.Length -gt 0) { $_.Substring(0, 1).ToUpper() + $_.Substring(1) } else { $_ }
    }) -join '-'
  if ($Label -like 'com.tickstream.*') { return "TickStream $name" }
  if ($Label -like 'com.stocktrader.*') { return "StockTrader $name" }
  return "SignalDeck $name"
}

$created = 0; $updated = 0; $skipped = 0; $managed = @()

foreach ($f in (Get-ChildItem (Join-Path $repo 'ops') -Filter 'com.*.plist' | Sort-Object Name)) {
  try { $xml = [xml](Get-Content $f.FullName -Raw) }
  catch { Write-Output ("SKIP   {0,-34} unparseable: {1}" -f $f.Name, $_.Exception.Message); $skipped++; continue }

  $d = Get-PlistDict $xml.plist.dict
  $label = if ($d.ContainsKey('Label')) { ConvertTo-Value $d['Label'] } else { $f.BaseName }
  $task = Get-TaskName $label

  $pargs = if ($d.ContainsKey('ProgramArguments')) { @(ConvertTo-Value $d['ProgramArguments']) } else { @() }
  $sh = $pargs | Where-Object { $_ -is [string] -and $_ -match '\.sh$' } | Select-Object -First 1
  if (-not $sh) {
    Write-Output ("SKIP   {0,-34} no shell script in ProgramArguments" -f $task)
    $skipped++; continue
  }
  # Rewrite any absolute macOS path onto THIS repo.
  $scriptPosix = "$repoPosix/ops/" + (Split-Path $sh -Leaf)
  $rest = @($pargs | Where-Object {
      $_ -is [string] -and $_ -ne $sh -and $_ -notmatch '\.sh$' -and $_ -notmatch '(^|/)(bash|sh)$'
    })
  $argLine = ('"{0}"' -f $scriptPosix) + $(if ($rest.Count) { ' ' + ($rest -join ' ') } else { '' })

  # --- triggers -----------------------------------------------------------
  $triggers = @(); $when = @()
  if ($d.ContainsKey('StartCalendarInterval')) {
    $cal = ConvertTo-Value $d['StartCalendarInterval']
    $items = if ($cal -is [System.Collections.IDictionary]) { @($cal) } else { @($cal) }
    foreach ($it in $items) {
      $h = if ($it.ContainsKey('Hour')) { [int](ConvertTo-Value $it['Hour']) } else { 0 }
      $m = if ($it.ContainsKey('Minute')) { [int](ConvertTo-Value $it['Minute']) } else { 0 }
      $at = (Get-Date -Hour $h -Minute $m -Second 0)
      if ($it.ContainsKey('Weekday')) {
        $dow = [System.DayOfWeek]([int](ConvertTo-Value $it['Weekday']) % 7)
        $triggers += New-ScheduledTaskTrigger -Weekly -DaysOfWeek $dow -At $at
        $when += ('{0} {1:HH:mm}' -f $dow, $at)
      } else {
        $triggers += New-ScheduledTaskTrigger -Daily -At $at
        $when += ('daily {0:HH:mm}' -f $at)
      }
    }
  }
  if ($d.ContainsKey('StartInterval')) {
    $sec = [int](ConvertTo-Value $d['StartInterval'])
    $triggers += New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval ([TimeSpan]::FromSeconds($sec))
    $when += "every ${sec}s"
  }
  if (-not $triggers.Count) {
    $keep = $d.ContainsKey('KeepAlive') -and (ConvertTo-Value $d['KeepAlive'])
    $load = $d.ContainsKey('RunAtLoad') -and (ConvertTo-Value $d['RunAtLoad'])
    if ($keep -or $load) { $triggers += New-ScheduledTaskTrigger -AtLogOn; $when += 'at logon' }
  }
  if (-not $triggers.Count) {
    Write-Output ("SKIP   {0,-34} on-demand only (no schedule to translate)" -f $task)
    $skipped++; continue
  }

  $exists = $null -ne (Get-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue)
  $managed += $task
  if ($exists) { $updated++ } else { $created++ }

  if ($Install) {
    $action = New-ScheduledTaskAction -Execute $bash -Argument $argLine -WorkingDirectory $repo
    $set = New-ScheduledTaskSettingsSet -StartWhenAvailable -AllowStartIfOnBatteries `
      -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::FromHours(6))
    Register-ScheduledTask -TaskName $task -Action $action -Trigger $triggers `
      -Settings $set -RunLevel Limited -Force | Out-Null
    Write-Output ("{0} {1,-34} {2}" -f $(if ($exists) { 'UPDATE' } else { 'CREATE' }), $task, ($when -join ', '))
  } else {
    Write-Output ("{0,-21} {1,-34} {2}" -f $(if ($exists) { 'exists (would update)' } else { 'would create' }), $task, ($when -join ', '))
  }
}

if ($Remove) {
  foreach ($t in $managed) {
    if (Get-ScheduledTask -TaskName $t -ErrorAction SilentlyContinue) {
      Unregister-ScheduledTask -TaskName $t -Confirm:$false
      Write-Output "REMOVED $t"
    }
  }
  Write-Output "removed $($managed.Count) managed task(s)"
  exit 0
}

Write-Output ''
Write-Output ("{0}: {1} new, {2} existing, {3} skipped" -f `
  $(if ($Install) { 'installed' } else { 'would install' }), $created, $updated, $skipped)
if (-not $Install) { Write-Output 'Nothing was changed. Re-run with -Install to apply.' }
