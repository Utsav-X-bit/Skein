# Inspect the Windows Firewall rules owned by Skein.
# Use -Repair to run the daemon's setup verb first.

param(
    [string]$Bin = "",
    [switch]$Repair
)

. (Join-Path $PSScriptRoot "common.ps1")
Assert-Admin
$Bin = Resolve-Bin $Bin
$state = Get-ServiceState
if ($state -eq "missing" -and -not $Repair) {
    Write-Host "SKIP firewall checks (skein service is not installed)"
    exit 0
}

if ($Repair) {
    $skein = Join-Path $Bin "skein.exe"
    $config = Join-Path $env:ProgramData "Skein\skein.toml"
    if (-not (Test-Path $config)) { throw "system config not found: $config" }
    $r = Invoke-Native $skein @("-c", $config, "setup")
    Assert-Exit "skein setup" $r.Code
    Write-Host $r.Output
}

$expected = @(
    @{ Name = "Skein underlay"; Direction = "Inbound" },
    @{ Name = "Skein overlay"; Direction = "Inbound" },
    @{ Name = "Skein overlay out"; Direction = "Outbound" }
)
foreach ($item in $expected) {
    $rules = @(Get-NetFirewallRule -DisplayName $item.Name -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq $item.Name })
    if ($rules.Count -eq 0) { throw "missing firewall rule: $($item.Name)" }
    if ($rules.Count -ne 1) { throw "duplicate firewall rules: $($item.Name)" }
    $rule = $rules[0]
    if ([string]$rule.Enabled -ne "True") { throw "firewall rule disabled: $($item.Name)" }
    if ([string]$rule.Action -ne "Allow") { throw "firewall rule is not Allow: $($item.Name)" }
    if ([string]$rule.Direction -ne $item.Direction) {
        throw "firewall rule $($item.Name) direction is $($rule.Direction), want $($item.Direction)"
    }
    Write-Host "rule ok: $($item.Name) [$($rule.Direction)]"
}

$legacy = @(Get-NetFirewallRule -DisplayName "skein" -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq "skein" })
if ($legacy) { throw "legacy duplicate firewall rule 'skein' is still present" }

Write-Host "firewall checks passed"

exit 0
