# Private local workspace: the proxy exception and loopback bind are inseparable.
$ErrorActionPreference = 'Stop'
$sdRepo = Split-Path -Parent $PSScriptRoot
$env:SIGNALDECK_LOCAL_ONLY_PROXY = '1'
Set-Location -LiteralPath (Join-Path $sdRepo 'web')
& 'C:\Program Files\nodejs\node.exe' (Join-Path $sdRepo 'web\node_modules\next\dist\bin\next') start -H 127.0.0.1 -p 3000
exit $LASTEXITCODE
