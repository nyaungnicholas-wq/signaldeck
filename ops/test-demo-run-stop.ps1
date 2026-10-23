# Proves ops\demo-run.ps1 -Stop stops only the demo's own processes. pids.json
# outlives a crash or reboot and Windows reuses process ids, so a recorded id can
# belong to the live daemon or web by the time -Stop runs. Harmless node sleepers
# stand in for every process; the demo's scratch dir is redirected via TEMP.
$ErrorActionPreference = 'Stop'
$script:fails = 0
function Check([string]$what, [bool]$ok) {
    if ($ok) { Write-Host ("  ok   {0}" -f $what) }
    else { Write-Host ("  FAIL {0}" -f $what) -ForegroundColor Red; $script:fails++ }
}

$tmp = Join-Path $env:TEMP ("demostop-" + (New-Guid).ToString().Substring(0,8))
$state = Join-Path $tmp 'signaldeck-demo'
New-Item -ItemType Directory -Path $state -Force | Out-Null
$pidFile = Join-Path $state 'pids.json'
$script = Join-Path $PSScriptRoot 'demo-run.ps1'
$procs = @()

function Sleeper([string]$tail) {
    $p = Start-Process -FilePath 'node' -PassThru -WindowStyle Hidden -ArgumentList ('-e "setInterval(()=>{},1000)" ' + $tail)
    $script:procs += $p
    return $p
}

function StopDemo([hashtable]$rec) {
    $rec | ConvertTo-Json | Set-Content -LiteralPath $pidFile -Encoding ASCII
    $oldTemp = $env:TEMP
    try {
        $env:TEMP = $tmp
        $local:ErrorActionPreference = 'Continue'
        $out = & powershell -NoProfile -NonInteractive -File $script -Stop 2>&1 | Out-String
        return [pscustomobject]@{ Code = $LASTEXITCODE; Out = $out }
    } finally {
        $env:TEMP = $oldTemp
    }
}

$alive = { param($p) Start-Sleep -Milliseconds 300; -not $p.WaitForExit(2000) }

try {
    # Case 1: recorded ids now belong to other processes
    $other = Sleeper 'signaldeckd.exe serve'
    $liveWeb = Sleeper '"C:\sd\web\node_modules\next\dist\bin\next" start "C:\sd\web" -p 8323 -H 127.0.0.1'
    $r = StopDemo @{ stub = $other.Id; web = $liveWeb.Id; stubPort = 18352; webPort = 18351 }
    Check 'case1 exit code 0' ($r.Code -eq 0)
    Check 'case1 other still alive' (& $alive $other)
    Check 'case1 liveWeb still alive' (& $alive $liveWeb)
    $m = [regex]::Matches($r.Out, 'is not the demo any more')
    Check 'case1 two "is not the demo any more" lines' ($m.Count -eq 2)
    Check 'case1 pids.json removed' (-not (Test-Path -LiteralPath $pidFile))

    # Case 2: recorded ids are still the demo's own
    $stub = Sleeper '"C:\sd\tools\demo\demo_daemon.py" --port 18352 --registry "C:\x\r.json"'
    $web = Sleeper '"C:\sd\web\node_modules\next\dist\bin\next" start "C:\sd\web" -p 18351 -H 127.0.0.1'
    $r = StopDemo @{ stub = $stub.Id; web = $web.Id; stubPort = 18352; webPort = 18351 }
    Check 'case2 stub stopped' (-not (& $alive $stub))
    Check 'case2 web stopped' (-not (& $alive $web))
    Check 'case2 output has "demo stub stopped"' ($r.Out -match 'demo stub stopped')
    Check 'case2 output has "demo web stopped"' ($r.Out -match 'demo web stopped')
}
catch {
    Check ("threw: " + $_.Exception.Message) $false
}
finally {
    foreach ($p in $procs) {
        if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
    }
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

if ($script:fails -gt 0) {
    Write-Host ("FAILED: {0} check(s)" -f $script:fails) -ForegroundColor Red
    exit 1
}
Write-Host 'DEMO STOP SELFTEST OK'
exit 0