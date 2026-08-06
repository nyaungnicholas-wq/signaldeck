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
      # launchd fires on every field a StartCalendarInterval dict OMITS, so a
      # dict carrying only Minute means "every hour at :MM", not "daily at
      # 00:MM". No plist in ops/ does that today; say so rather than silently
      # registering a schedule 24x slower than the plist asks for.
      if (-not $it.ContainsKey('Hour')) {
        Write-Output ("WARN   {0,-34} no Hour in StartCalendarInterval (launchd runs this HOURLY); registered daily {1:HH:mm}" -f $task, $at)
      }
      $untranslated = @($it.Keys | Where-Object { $_ -notin @('Day', 'Hour', 'Minute', 'Weekday') })
      if ($untranslated.Count) {
        Write-Output ("WARN   {0,-34} StartCalendarInterval key(s) ignored: {1}" -f $task, ($untranslated -join ', '))
      }
      if ($it.ContainsKey('Day')) {
        # Day-of-month. Until 2026-08-06 there was no branch for it, so a
        # monthly plist fell through to -Daily: com.signaldeck.revalidation asks
        # for the 1st at 03:20 and the live task ran every night (LastRunTime
        # 08-06 03:20, NextRunTime 08-07 03:20).
        #
        # New-ScheduledTaskTrigger has no -Monthly, so the trigger is built as a
        # CIM instance. The property shapes are load-bearing and are NOT the ones
        # most examples use: MSFT_TaskMonthlyTrigger declares MonthOfYear
        # (singular) and a scalar UInt16 DaysOfMonth BITMASK -- day N is
        # 1 shl (N-1), confirmed against IMonthlyTrigger, which renders
        # DaysOfMonth=32768 as <Day>16</Day>. The copied-everywhere
        # MonthsOfYear/uint32[] form fails to bind. A UInt16 mask stops at 16.
        $day = [int](ConvertTo-Value $it['Day'])
        if ($day -lt 1 -or $day -gt 16) {
          Write-Output ("WARN   {0,-34} Day {1} is outside the 1-16 a UInt16 day mask can carry; no trigger registered for it" -f $task, $day)
          continue
        }
        $mt = New-CimInstance -ClassName MSFT_TaskMonthlyTrigger `
          -Namespace Root/Microsoft/Windows/TaskScheduler -ClientOnly -Property @{
            DaysOfMonth   = [uint16](1 -shl ($day - 1))
            MonthOfYear   = [uint16]4095   # every month
            StartBoundary = $at.ToString('yyyy-MM-ddTHH:mm:ss')
            Enabled       = $true
          }
        # -Trigger binds on PSTypeName, and a ClientOnly instance of a subclass
        # does not advertise the base type on its own.
        $mt.PSTypeNames.Insert(0, 'Microsoft.Management.Infrastructure.CimInstance#MSFT_TaskTrigger')
        $triggers += $mt
        $when += ('monthly day {0} {1:HH:mm}' -f $day, $at)
      } elseif ($it.ContainsKey('Weekday')) {
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
    # The monthly trigger is assembled by hand from a CIM class whose property
    # shapes are not documented alongside the cmdlet, and a wrong shape can
    # register quietly as something else -- the exact failure this branch exists
    # to end. Read it back rather than trust it.
    if (($when -join ',') -like '*monthly*') {
      $kinds = @((Get-ScheduledTask -TaskName $task).Triggers.CimClass.CimClassName)
      if ($kinds -notcontains 'MSFT_TaskMonthlyTrigger') {
        Write-Output ("FAIL   {0,-34} monthly trigger did not survive registration (got: {1})" -f $task, ($kinds -join ', '))
      }
    }
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
