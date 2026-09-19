<#
.SYNOPSIS
Negative control for ops/web-assets-check.ps1: a deployment with one chunk
deleted must FAIL the check while /login still answers 200.

.DESCRIPTION
The defect this guards against is not "the site is down". It is "the site
answers 200 and renders without its stylesheet", which is what port 3000 did
for 24 hours on 2026-09-13 while ops/web-guard.ps1 logged "3000 ok" on every
five-minute cycle, because the only thing it measured was the status code of
/login.

So the assertion that matters is the CONJUNCTION:

    /login  -> 200        (the old guard's whole test, still passing)
    assets  -> FAIL       (the new check, catching it)

A test that only asserted the failure would pass against a server that was
simply down, which is the case the old guard already handled.

ISOLATION. This never touches web/.next or either live instance. It copies the
current build to web/.next-test, serves it on a port nothing else uses, and
deletes the chunk from the COPY. next.config.ts reads SIGNALDECK_DIST_DIR for
exactly this. Next requires distDir to stay inside the project directory, which
is why the copy lives under web/ and not in a temp folder.

Run:  powershell -NoProfile -ExecutionPolicy Bypass -File ops\test-web-assets-check.ps1
#>
[CmdletBinding()]
param(
    [int]$Port = 8399,
    [switch]$KeepBuild
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$web = Join-Path $repo 'web'
$src = Join-Path $web '.next'
$dst = Join-Path $web '.next-test'
$check = Join-Path $PSScriptRoot 'web-assets-check.ps1'

$failures = @()
$proc = $null

function Assert([bool]$cond, [string]$what) {
    if ($cond) { Write-Output ("  PASS  " + $what) }
    else { Write-Output ("  FAIL  " + $what); $script:failures += $what }
}

try {
    if (-not (Test-Path -LiteralPath (Join-Path $src 'BUILD_ID'))) {
        Write-Output "test-web-assets-check: no build at $src (run: cd web; npm run build)"
        exit 2
    }
    if ((Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)) {
        Write-Output "test-web-assets-check: port $Port is already in use; pass -Port to pick another"
        exit 2
    }

    Write-Output "1. isolate the build -> $dst"
    if (Test-Path -LiteralPath $dst) { Remove-Item -LiteralPath $dst -Recurse -Force }
    Copy-Item -LiteralPath $src -Destination $dst -Recurse -Force

    Write-Output "2. serve the INTACT copy on :$Port"
    # Captured, not discarded: a server that fails to boot must say why here
    # rather than surface as a bare "never answered".
    $outLog = Join-Path $env:TEMP 'sd-assets-test.out.log'
    $errLog = Join-Path $env:TEMP 'sd-assets-test.err.log'
    $node = (Get-Command node -ErrorAction Stop).Source
    $nextBin = Join-Path $web 'node_modules\next\dist\bin\next'
    $env:SIGNALDECK_DIST_DIR = '.next-test'
    # QUOTE THE PATH. Start-Process re-joins -ArgumentList on spaces, and this
    # repo lives under "Desktop\claude code", so an unquoted $nextBin reached
    # node as the argument "C:\Users\Nicholas_N\Desktop\claude" and it died with
    # MODULE_NOT_FOUND on that truncation.
    $proc = Start-Process -FilePath $node `
        -ArgumentList @(('"' + $nextBin + '"'), 'start', '-H', '127.0.0.1', '-p', "$Port") `
        -WorkingDirectory $web -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput $outLog -RedirectStandardError $errLog

    $up = $false
    for ($i = 0; $i -lt 60; $i++) {
        Start-Sleep -Seconds 1
        try {
            $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/login" -UseBasicParsing -TimeoutSec 5
            if ([int]$r.StatusCode -eq 200) { $up = $true; break }
        } catch { }
    }
    if (-not $up) {
        Write-Output "test-web-assets-check: the isolated server never answered on :$Port"
        foreach ($f in @($outLog, $errLog)) {
            if (Test-Path -LiteralPath $f) {
                Write-Output ("--- " + (Split-Path -Leaf $f) + " ---")
                Get-Content -LiteralPath $f -Tail 20 | ForEach-Object { Write-Output ("  " + $_) }
            }
        }
        exit 2
    }

    & powershell -NoProfile -ExecutionPolicy Bypass -File $check -Port $Port -Quiet | Out-Null
    Assert ($LASTEXITCODE -eq 0) "intact isolated build passes the check (exit 0)"

    # --- the negative control -----------------------------------------------
    Write-Output "3. delete ONE stylesheet chunk from the copy and re-check"
    $victim = Get-ChildItem -LiteralPath (Join-Path $dst 'static\chunks') -Filter '*.css' |
        Select-Object -First 1
    if (-not $victim) {
        Write-Output "test-web-assets-check: no .css chunk in the build to delete"
        exit 2
    }
    Remove-Item -LiteralPath $victim.FullName -Force
    Write-Output ("   deleted " + $victim.Name)

    # /login must STILL answer 200. This is the half the old guard measured,
    # and it has to keep passing or the test proves nothing about the gap.
    $loginCode = 0
    try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/login" -UseBasicParsing -TimeoutSec 10
        $loginCode = [int]$r.StatusCode
    } catch {
        if ($_.Exception.Response) { $loginCode = [int]$_.Exception.Response.StatusCode }
    }
    Assert ($loginCode -eq 200) "/login still answers 200 with the chunk deleted (the old guard's blind spot)"

    $out = & powershell -NoProfile -ExecutionPolicy Bypass -File $check -Port $Port 2>&1
    $rc = $LASTEXITCODE
    Assert ($rc -eq 1) "asset check FAILS on the damaged build (exit 1, got $rc)"
    Assert (($out -join "`n") -match [regex]::Escape($victim.Name)) "the failure names the deleted chunk"
}
finally {
    if ($proc -and -not $proc.HasExited) {
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item Env:\SIGNALDECK_DIST_DIR -ErrorAction SilentlyContinue
    if (-not $KeepBuild -and (Test-Path -LiteralPath $dst)) {
        Remove-Item -LiteralPath $dst -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Write-Output ''
if ($failures.Count -gt 0) {
    Write-Output ("test-web-assets-check: FAILED ({0})" -f $failures.Count)
    exit 1
}
Write-Output 'test-web-assets-check: OK - 4 assertions passed'
exit 0
