# Download the pinned Wintun release and install wintun.dll next to the
# Windows binaries.
#
#   .\scripts\fetch-wintun.ps1 [-Arch amd64] [-Dest dist]
#
# Arch: amd64 (default), arm64, x86, arm
# Dest: directory to place wintun.dll and its license (default: dist)

param(
    [string]$Arch = "amd64",
    [string]$Dest = "dist"
)

$ErrorActionPreference = "Stop"

$version = "0.14.1"
$sha256 = "07C256185D6EE3652E09FA55C0B673E2624B565E02C4B9091C79CA7D2F24EF51"
$url = "https://www.wintun.net/builds/wintun-$version.zip"

$tmp = Join-Path $env:TEMP ("wintun-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "fetching wintun $version ($Arch)"
    Invoke-WebRequest -Uri $url -OutFile (Join-Path $tmp "wintun.zip") -UseBasicParsing

    $actual = (Get-FileHash (Join-Path $tmp "wintun.zip") -Algorithm SHA256).Hash
    if ($actual -ne $sha256) {
        throw "checksum mismatch: got $actual, want $sha256"
    }

    Expand-Archive -Path (Join-Path $tmp "wintun.zip") -DestinationPath $tmp -Force
    $src = Join-Path $tmp "wintun\bin\$Arch\wintun.dll"
    if (-not (Test-Path $src)) {
        throw "$Arch is not in the archive (amd64, arm64, x86, arm)"
    }

    New-Item -ItemType Directory -Force -Path $Dest | Out-Null
    Copy-Item $src (Join-Path $Dest "wintun.dll") -Force
    Copy-Item (Join-Path $tmp "wintun\LICENSE.txt") (Join-Path $Dest "wintun-LICENSE.txt") -Force
    $size = (Get-Item (Join-Path $Dest "wintun.dll")).Length
    Write-Host "installed $Dest\wintun.dll ($size bytes) and $Dest\wintun-LICENSE.txt"
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
