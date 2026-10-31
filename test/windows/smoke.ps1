# Skein Windows smoke test.
#
# Runs two daemons on one machine with distinct interface names and ports,
# verifies the direct link, pings across the overlay, and checks cleanup.
#
# Run from an elevated PowerShell in the directory that holds the binaries:
#   .\smoke.ps1

$ErrorActionPreference = "Stop"

function Assert-Admin {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "smoke.ps1 must run as Administrator"
    }
}

function Get-Identity($cfgPath) {
    $out = & .\skein.exe -c $cfgPath genkey
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
    Set-Content -Path $cfgPath -Value $body -Encoding utf8
}

Assert-Admin
if (-not (Test-Path ".\skeind.exe")) { throw "skeind.exe not found in the current directory" }
if (-not (Test-Path ".\skein.exe")) { throw "skein.exe not found in the current directory" }
if (-not (Test-Path ".\wintun.dll")) { throw "wintun.dll not found next to skeind.exe" }

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

Write-Config $cfgA "10.144.144.1" 11020 11021 "SkeinSmokeA" $idB.ID $idB.Pub "127.0.0.1:11030" "true" $psk
Write-Config $cfgB "10.144.144.2" 11030 11031 "SkeinSmokeB" $idA.ID $idA.Pub "127.0.0.1:11020" "false" $psk

$procA = $null
$procB = $null
$failed = $true
try {
    Write-Host "Starting daemons..."
    $procA = Start-Process -FilePath ".\skeind.exe" -ArgumentList "run -c `"$cfgA`"" -PassThru
    $procB = Start-Process -FilePath ".\skeind.exe" -ArgumentList "run -c `"$cfgB`"" -PassThru

    $deadline = (Get-Date).AddSeconds(30)
    $up = $false
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 500
        $json = & .\skein.exe -c $cfgA peers --json 2>$null
        if ($LASTEXITCODE -eq 0 -and ($json -join "") -match '"state"\s*:\s*"up"') {
            $up = $true
            break
        }
    }
    if (-not $up) { throw "daemons did not establish a direct link" }

    Write-Host "Overlay ping:"
    $ping = ping -n 2 10.144.144.2
    $ping | Write-Host
    if (-not ($ping -join "`n") -match "Received = 2") { throw "overlay ping failed" }

    Write-Host "Routes:"
    & .\skein.exe -c $cfgA routes
    if ($LASTEXITCODE -ne 0) { throw "routes command failed" }

    Write-Host "Stopping daemons..."
    Stop-Process -Id $procA.Id -Force
    Stop-Process -Id $procB.Id -Force
    $procA.WaitForExit()
    $procB.WaitForExit()
    $procA = $null
    $procB = $null

    Start-Sleep -Seconds 2
    $interfaces = netsh interface show interface
    foreach ($name in @("SkeinSmokeA", "SkeinSmokeB")) {
        if (($interfaces -join "`n") -match [regex]::Escape($name)) {
            throw "interface $name survived daemon shutdown"
        }
    }
    $failed = $false
    Write-Host "smoke test passed"
}
finally {
    foreach ($p in @($procA, $procB)) {
        if ($p -and -not $p.HasExited) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
    }
    Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
    if ($failed) { Write-Host "smoke test failed" }
}
