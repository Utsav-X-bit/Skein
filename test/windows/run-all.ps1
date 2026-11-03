# Run the non-destructive Windows test suite.
#
#   .\run-all.ps1 -Bin ..\..\dist
#   .\run-all.ps1 -SkipGui
#
# Requires elevation for the two-daemon smoke test and firewall inspection.

param(
    [string]$Bin = "",
    [switch]$SkipSmoke,
    [switch]$SkipGui
)

. (Join-Path $PSScriptRoot "common.ps1")
Assert-Admin
$Bin = Resolve-Bin $Bin

$tests = @()
if (-not $SkipSmoke) { $tests += "smoke.ps1" }
$tests += "cli.ps1"
$tests += "pipe.ps1"
$tests += "helper.ps1"
$tests += "service.ps1"
$tests += "firewall.ps1"
if (-not $SkipGui) { $tests += "gui.ps1" }

foreach ($test in $tests) {
    Write-Host ""
    Write-Host "=== $test ==="
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot $test) -Bin $Bin
    if ($LASTEXITCODE -ne 0) { throw "$test failed with exit code $LASTEXITCODE" }
}
Write-Host ""
Write-Host "all Windows tests passed"
