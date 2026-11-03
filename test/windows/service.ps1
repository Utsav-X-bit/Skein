# Read-only service checks.

param([string]$Bin = "")

. (Join-Path $PSScriptRoot "common.ps1")
$Bin = Resolve-Bin $Bin
$state = Get-ServiceState
if ($state -eq "missing") {
    Write-Host "SKIP service checks (skein service is not installed)"
    exit 0
}

$query = Invoke-Native "sc.exe" @("query", "skein")
Assert-Exit "sc query skein" $query.Code
Write-Host $query.Output

$qc = Invoke-Native "sc.exe" @("qc", "skein")
Assert-Exit "sc qc skein" $qc.Code
if ($qc.Output -notmatch "BINARY_PATH_NAME") { throw "sc qc did not report a binary path" }
if ($qc.Output -notmatch "skein") { throw "service binary path does not name skein" }

if ($state -eq "Running") {
    $skein = Join-Path $Bin "skein.exe"
    $r = Invoke-Native $skein @("status", "--json")
    Assert-Exit "skein status --json" $r.Code
    $status = $r.Output | ConvertFrom-Json
    if (-not $status.id) { throw "status has no node id" }
    Write-Host "service running: node $($status.id), connection $($status.connection.state)"
} else {
    Write-Host "service state: $state"
}

Write-Host "service checks passed"

exit 0
