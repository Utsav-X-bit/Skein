# Read-only checks for the fixed-verb helper.

param([string]$Bin = "")

. (Join-Path $PSScriptRoot "common.ps1")
$Bin = Resolve-Bin $Bin
$helper = Join-Path $Bin "skein-helper.exe"
if (-not (Test-Path $helper)) {
    $installed = Join-Path $env:ProgramFiles "Skein\skein-helper.exe"
    if (Test-Path $installed) { $helper = $installed } else { throw "skein-helper.exe not found" }
}

$r = Invoke-Native $helper @("status")
Assert-Exit "skein-helper status" $r.Code
$status = $r.Output | ConvertFrom-Json
if ($status.ok -ne $true) { throw "helper status was not ok: $($r.Output)" }
if (-not $status.state) { throw "helper status has no service state" }
Write-Host "helper status: $($status.state)"

$r = Invoke-Native $helper @("help")
Assert-Exit "skein-helper help" $r.Code
if ($r.Output -notmatch "skein-helper") { throw "helper help output missing usage" }

$r = Invoke-Native $helper @()
if ($r.Code -ne 2) { throw "skein-helper with no verb exited $($r.Code), want 2" }

Write-Host "helper checks passed"
