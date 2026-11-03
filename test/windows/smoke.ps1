# Skein Windows smoke test.
#
# Runs two daemons on one machine with distinct interface names and ports,
# verifies the direct link, pings across the overlay, and checks cleanup.
#
# Run from an elevated PowerShell:
#   .\smoke.ps1 -Bin ..\..\dist
# or from the directory that holds the binaries:
#   .\smoke.ps1

param(
    [string]$Bin = ""
)

$ErrorActionPreference = "Stop"

if (-not $Bin) {
    $Bin = (Get-Location).Path
    $fallback = Join-Path $PSScriptRoot "..\..\dist"
    if (-not (Test-Path (Join-Path $Bin "skein.exe")) -and (Test-Path (Join-Path $fallback "skein.exe"))) {
        $Bin = (Resolve-Path $fallback).Path
    }
} else {
    $Bin = (Resolve-Path $Bin).Path
}
$skeinExe = Join-Path $Bin "skein.exe"
$skeindExe = Join-Path $Bin "skeind.exe"
$wintunDll = Join-Path $Bin "wintun.dll"

function Assert-Admin {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "smoke.ps1 must run as Administrator"
    }
}

function Get-Identity($cfgPath) {
    $out = & $skeinExe -c $cfgPath genkey
    if ($LASTEXITCODE -ne 0) { throw "genkey failed for $cfgPath" }
    $id = [regex]::Match(($out -join "`n"), 'node id:\s+([0-9a-f]{16})').Groups[1].Value
    $pub = [regex]::Match(($out -join "`n"), 'public key:\s+(\S+)').Groups[1].Value
    if (-not $id -or -not $pub) { throw "could not parse genkey output" }
    return @{ ID = $id; Pub = $pub }
}

function Write-Config($cfgPath, $overlay, $port, $apiPort, $interface, $peerID, $peerPub, $peerAddr, $dialer, $psk) {
    $token = "4VFScqAStn6UYJtGDdyHG6SRYA9xCEHVANw2-iYqF1E"
    $body = @"
[node]
overlay_ipv4_prefix = "10.144.145.0/24"
overlay_ipv4 = "$overlay"
listen_port = $port
network_secret = "$psk"
mtu = 1280
interface = "$interface"

[api]
listen = "127.0.0.1:$apiPort"
token = "$token"

[discovery]
mdns = false

[[peer]]
id = "$peerID"
pubkey = "$peerPub"
dialer = $dialer
addrs = ["udp://$peerAddr"]
"@
    # PowerShell 5.1 writes a UTF-8 BOM with Set-Content; the TOML parser
    # rejects it, so write the bytes explicitly.
    [System.IO.File]::WriteAllText($cfgPath, $body, (New-Object System.Text.UTF8Encoding($false)))
}

Assert-Admin
if (-not (Test-Path $skeindExe)) { throw "skeind.exe not found in $Bin" }
if (-not (Test-Path $skeinExe)) { throw "skein.exe not found in $Bin" }
if (-not (Test-Path $wintunDll)) { throw "wintun.dll not found next to skeind.exe" }

$root = Join-Path $env:TEMP ("skein-smoke-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
$dirA = Join-Path $root "node a"
$dirB = Join-Path $root "node b"
New-Item -ItemType Directory -Force -Path $dirA, $dirB | Out-Null
# Config paths deliberately contain spaces.
$cfgA = Join-Path $dirA "skein config.toml"
$cfgB = Join-Path $dirB "skein config.toml"

$psk = "qUKSgFNEmpqJiO3DS_vmRaYdxCAY_AZ_H6Cnmdn6E_U"

Write-Host "Generating identities..."
$idA = Get-Identity $cfgA
$idB = Get-Identity $cfgB

Write-Config $cfgA "10.144.145.1" 11020 11021 "SkeinSmokeA" $idB.ID $idB.Pub "127.0.0.1:11030" "true" $psk
Write-Config $cfgB "10.144.145.2" 11030 11031 "SkeinSmokeB" $idA.ID $idA.Pub "127.0.0.1:11020" "false" $psk

$procA = $null
$procB = $null
$logA = Join-Path $root "node-a.log"
$logB = Join-Path $root "node-b.log"
$errA = Join-Path $root "node-a.err.log"
$errB = Join-Path $root "node-b.err.log"
$failed = $true
try {
    Write-Host "Starting daemons..."
    $procA = Start-Process -FilePath $skeindExe -ArgumentList "run -c `"$cfgA`"" -PassThru -RedirectStandardOutput $logA -RedirectStandardError $errA
    $procB = Start-Process -FilePath $skeindExe -ArgumentList "run -c `"$cfgB`"" -PassThru -RedirectStandardOutput $logB -RedirectStandardError $errB

    # Wait for each daemon's API before asking for peers; otherwise a failed
    # daemon looks like a connection error instead of an exited process.
    $deadline = (Get-Date).AddSeconds(30)
    $readyA = $false
    $readyB = $false
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 500
        if ($procA.HasExited) { throw "daemon A exited during startup; see $errA" }
        if ($procB.HasExited) { throw "daemon B exited during startup; see $errB" }
        if (-not $readyA) {
            $null = & $skeinExe -c $cfgA status --json 2>&1
            if ($LASTEXITCODE -eq 0) { $readyA = $true }
        }
        if (-not $readyB) {
            $null = & $skeinExe -c $cfgB status --json 2>&1
            if ($LASTEXITCODE -eq 0) { $readyB = $true }
        }
        if ($readyA -and $readyB) { break }
    }
    if (-not $readyA) { throw "daemon A API did not become ready; see $errA" }
    if (-not $readyB) { throw "daemon B API did not become ready; see $errB" }

    $deadline = (Get-Date).AddSeconds(30)
    $up = $false
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 500
        $json = & $skeinExe -c $cfgA peers --json 2>&1
        if ($LASTEXITCODE -eq 0 -and ($json -join "") -match '"state"\s*:\s*"up"') {
            $up = $true
            break
        }
    }
    if (-not $up) { throw "daemons did not establish a direct link" }

    Write-Host "Overlay ping:"
    $ping = ping -n 2 10.144.145.2
    $ping | Write-Host
    if (-not ($ping -join "`n") -match 'Received\s*=\s*[1-9]') { throw "overlay ping failed" }

    Write-Host "Routes:"
    & $skeinExe -c $cfgA routes
    if ($LASTEXITCODE -ne 0) { throw "routes command failed" }

    Write-Host "Stopping daemons..."
    Stop-Process -Id $procA.Id -Force
    Stop-Process -Id $procB.Id -Force
    $procA.WaitForExit()
    $procB.WaitForExit()
    $procA = $null
    $procB = $null

    # Wintun removes adapters asynchronously after the process exits.
    $cleanupDeadline = (Get-Date).AddSeconds(10)
    foreach ($name in @("SkeinSmokeA", "SkeinSmokeB")) {
        while ($true) {
            $interfaces = netsh interface show interface
            if (($interfaces -join "`n") -notmatch [regex]::Escape($name)) { break }
            if ((Get-Date) -ge $cleanupDeadline) {
                throw "interface $name survived daemon shutdown"
            }
            Start-Sleep -Milliseconds 500
        }
    }
    $failed = $false
    Write-Host "smoke test passed"
}
finally {
    foreach ($p in @($procA, $procB)) {
        if ($p -and -not $p.HasExited) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
    }
    if ($failed) {
        foreach ($log in @($logA, $errA, $logB, $errB)) {
            if (Test-Path $log) {
                Write-Host "--- $log ---"
                Get-Content $log -ErrorAction SilentlyContinue | Write-Host
            }
        }
    }
    Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
    if ($failed) { Write-Host "smoke test failed" }
}
