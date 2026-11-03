# Verify the Windows named-pipe control API used by the desktop and CLI.

param([string]$Bin = "")

. (Join-Path $PSScriptRoot "common.ps1")
$null = Resolve-Bin $Bin
$state = Get-ServiceState
if ($state -ne "Running") {
    Write-Host "SKIP pipe check (service state: $state)"
    exit 0
}

$pipe = New-Object System.IO.Pipes.NamedPipeClientStream(".", "skein-control", [System.IO.Pipes.PipeDirection]::InOut)
try {
    $pipe.Connect(5000)
    $request = "GET /api/v1/status HTTP/1.1`r`nHost: skein`r`nConnection: close`r`n`r`n"
    $bytes = [Text.Encoding]::ASCII.GetBytes($request)
    $pipe.Write($bytes, 0, $bytes.Length)
    $pipe.Flush()

    $reader = New-Object IO.StreamReader($pipe, [Text.Encoding]::UTF8)
    $response = $reader.ReadToEnd()
    if ($response -notmatch "HTTP/1\.1 200") { throw "pipe status failed: $response" }
    $body = ($response -split "`r`n`r`n", 2)[1]
    $json = $body | ConvertFrom-Json
    if (-not $json.id) { throw "pipe status response has no node id" }
    Write-Host "pipe check passed (node $($json.id), connection $($json.connection.state))"
} finally {
    $pipe.Dispose()
}

exit 0
