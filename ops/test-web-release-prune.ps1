# Proves Remove-StalePrevBuilds (ops/web-release.ps1) keeps the newest two
# .next-prev-* builds, never removes the rollback build logs/web-release.json
# names, and removes nothing when that record cannot be read.
#
# The regression: every promote moved the previous build aside and nothing ever
# deleted one; ten web\.next-prev-* dirs and 4.9 GB had piled up by 2026-10-02.
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

$fails = 0
function Check([string]$what, [bool]$ok) {
  if ($ok) { Write-Host "  ok   $what" }
  else { Write-Host "  FAIL $what" -ForegroundColor Red; $script:fails++ }
}

$root = Join-Path $env:TEMP ("webprune-{0}" -f [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $root -Force | Out-Null
. "$PSScriptRoot\web-release.ps1" -SelfTestOnly -LogPath (Join-Path $root 'release.log')

function NewCase([string]$name, [string[]]$dirs) {
  $case = Join-Path $root $name
  New-Item -ItemType Directory -Path $case -Force | Out-Null
  foreach ($d in $dirs) {
    $dirPath = Join-Path $case $d
    New-Item -ItemType Directory -Path $dirPath -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $dirPath 'BUILD_ID') -Value 'x' -Encoding UTF8 | Out-Null
  }
  return $case
}
function Names([string]$case) {
  return ((@(Get-ChildItem -LiteralPath $case -Directory | ForEach-Object { $_.Name }) | Sort-Object) -join ',')
}
function Joined([string[]]$list) {
  return ((@($list) | Sort-Object) -join ',')
}

# Case 1: protects-record
$case1 = NewCase 'protects-record' @('.next', '.next-release-abc', '.next-prev-20261001-010101', '.next-prev-20261001-020202', '.next-prev-20261001-030303', '.next-prev-20261002-040404', '.next-prev-20261002-050505')
# Also create a plain file (not a dir) named .next-prev-20261002-060606
Set-Content -LiteralPath (Join-Path $case1 '.next-prev-20261002-060606') -Value 'x' -Encoding UTF8 | Out-Null
$rec1 = Join-Path $root 'r1.json'
$protected = Join-Path $case1 '.next-prev-20261001-010101'
Set-Content -LiteralPath $rec1 -Value (@{ keptPrevious = $protected } | ConvertTo-Json) -Encoding UTF8
$r1 = @(Remove-StalePrevBuilds -WebDir $case1 -RecordPath $rec1 -Keep 2)
Check "removed list matches expected" ((Joined $r1) -eq (Joined @('.next-prev-20261001-020202', '.next-prev-20261001-030303')))
Check "remaining dirs correct" ((Names $case1) -eq (Joined @('.next', '.next-release-abc', '.next-prev-20261001-010101', '.next-prev-20261002-040404', '.next-prev-20261002-050505')))
Check "plain file still exists" (Test-Path -LiteralPath (Join-Path $case1 '.next-prev-20261002-060606'))
$r1b = @(Remove-StalePrevBuilds -WebDir $case1 -RecordPath $rec1 -Keep 2)
Check "second prune removes nothing" ($r1b.Count -eq 0)

# Case 2: unreadable-record
$case2 = NewCase 'unreadable-record' @('.next-prev-20261001-010101', '.next-prev-20261001-020202', '.next-prev-20261001-030303', '.next-prev-20261001-040404')
$rec2 = Join-Path $root 'r2.json'
Set-Content -LiteralPath $rec2 -Value '{not json' -Encoding UTF8
$r2 = @(Remove-StalePrevBuilds -WebDir $case2 -RecordPath $rec2 -Keep 2)
Check "no dirs removed when record unreadable" ($r2.Count -eq 0)
Check "dirs unchanged when record unreadable" ((Names $case2) -eq (Joined @('.next-prev-20261001-010101', '.next-prev-20261001-020202', '.next-prev-20261001-030303', '.next-prev-20261001-040404')))

# Case 3: empty-record
$case3 = NewCase 'empty-record' @('.next-prev-20261001-010101', '.next-prev-20261001-020202', '.next-prev-20261001-030303', '.next-prev-20261001-040404')
$rec3 = Join-Path $root 'r3.json'
Set-Content -LiteralPath $rec3 -Value '' -Encoding UTF8
$r3 = @(Remove-StalePrevBuilds -WebDir $case3 -RecordPath $rec3 -Keep 2)
Check "no dirs removed when record empty" ($r3.Count -eq 0)
Check "dirs unchanged when record empty" ((Names $case3) -eq (Joined @('.next-prev-20261001-010101', '.next-prev-20261001-020202', '.next-prev-20261001-030303', '.next-prev-20261001-040404')))

# Case 4: no-record
$case4 = NewCase 'no-record' @('.next-prev-20261001-010101', '.next-prev-20261001-020202', '.next-prev-20261001-030303')
$rec4 = Join-Path $root 'missing.json' # never created
$r4 = @(Remove-StalePrevBuilds -WebDir $case4 -RecordPath $rec4 -Keep 2)
Check "removed oldest dir when no record" ((Joined $r4) -eq (Joined @('.next-prev-20261001-010101')))
Check "remaining two dirs when no record" ((Names $case4) -eq (Joined @('.next-prev-20261001-020202', '.next-prev-20261001-030303')))

# Case 5: none-record
$case5 = NewCase 'none-record' @('.next-prev-20261001-010101', '.next-prev-20261001-020202', '.next-prev-20261001-030303')
$rec5 = Join-Path $root 'r5.json'
Set-Content -LiteralPath $rec5 -Value (@{ keptPrevious = '(none)' } | ConvertTo-Json) -Encoding UTF8
$r5 = @(Remove-StalePrevBuilds -WebDir $case5 -RecordPath $rec5 -Keep 2)
Check "removed oldest dir when keptPrevious=(none)" ((Joined $r5) -eq (Joined @('.next-prev-20261001-010101')))
Check "remaining two dirs when keptPrevious=(none)" ((Names $case5) -eq (Joined @('.next-prev-20261001-020202', '.next-prev-20261001-030303')))

# Case 6: few
$case6 = NewCase 'few' @('.next-prev-20261001-010101', '.next-prev-20261001-020202')
$rec6 = Join-Path $root 'missing6.json' # never created
$r6 = @(Remove-StalePrevBuilds -WebDir $case6 -RecordPath $rec6 -Keep 2)
Check "no dirs removed when fewer than Keep+1" ($r6.Count -eq 0)

Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
if ($fails) {
  Write-Host "$fails check(s) failed" -ForegroundColor Red
  exit 1
}
Write-Host "all web-release prune checks passed"