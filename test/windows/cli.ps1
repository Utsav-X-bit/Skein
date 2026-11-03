# Read-only CLI checks against the installed service control pipe.
# Run elevated after the Skein service is running.

param([string]$Bin = "")

. (Join-Path $PSScriptRoot "common.ps1")
$Bin = Resolve-Bin $Bin
$skein = Join-Path $Bin "skein.exe"

$r = Invoke-Native $skein @("version")
Assert-Exit "skein version" $r.Code
if ($r.Output -notmatch "skein ") { throw "unexpected version output: $($r.Output)" }

$r = Invoke-Native $skein @("--help")
Assert-Exit "skein --help" $r.Code
if ($r.Output -notmatch "Usage:") { throw "help output did not contain Usage" }

$state = Get-ServiceState
if ($state -ne "Running") {
    Write-Host "SKIP service-backed CLI checks (service state: $state)"
    exit 0
}

$r = Invoke-Native $skein @("status", "--json")
Assert-Exit "skein status --json" $r.Code
$status = $r.Output | ConvertFrom-Json
if (-not $status.connection.state) { throw "status has no connection state" }

$r = Invoke-Native $skein @("peers", "--json")
Assert-Exit "skein peers --json" $r.Code
$null = $r.Output | ConvertFrom-Json

$r = Invoke-Native $skein @("routes", "--json")
Assert-Exit "skein routes --json" $r.Code
$null = $r.Output | ConvertFrom-Json

$r = Invoke-Native $skein @("dashboard", "--no-motion")
Assert-Exit "skein dashboard --no-motion" $r.Code
if ($r.Output -notmatch "SKEIN") { throw "dashboard output did not contain the Skein mark" }

Write-Host "cli checks passed (service: $state, connection: $($status.connection.state))"
