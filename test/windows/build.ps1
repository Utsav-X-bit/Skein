# Build all Windows artifacts and verify the bundle.
#
#   .\build.ps1
#   .\build.ps1 -Out C:\Skein -SkipDesktop -SkipWintun
#
# Requires Go, and (unless -SkipDesktop) the Wails 3 CLI with WebView2.

param(
    [string]$Out = (Join-Path $PSScriptRoot "..\..\dist"),
    [switch]$SkipDesktop,
    [switch]$SkipWintun
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "common.ps1")
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$Out = [System.IO.Path]::GetFullPath($Out)
New-Item -ItemType Directory -Force -Path $Out | Out-Null

function Require-Command($Name, $Hint) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "$Name not found. $Hint"
    }
}

Require-Command "go" "Install Go and add it to PATH."
$goBin = Join-Path (& go env GOPATH) "bin"
if (Test-Path $goBin) { $env:Path = "$goBin;$env:Path" }
Push-Location $root
try {
    Write-Host "Building CLI, daemon, and helper..."
    & go build -trimpath -o (Join-Path $Out "skein.exe") .\cmd\skein
    Assert-Exit "go build skein" $LASTEXITCODE
    & go build -trimpath -o (Join-Path $Out "skeind.exe") .\cmd\skeind
    Assert-Exit "go build skeind" $LASTEXITCODE
    & go build -trimpath -o (Join-Path $Out "skein-helper.exe") .\cmd\skein-helper
    Assert-Exit "go build skein-helper" $LASTEXITCODE

    if (-not $SkipDesktop) {
        Require-Command "wails3" "Install it with: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0"
        Push-Location (Join-Path $root "gui")
        try {
            Write-Host "Building desktop app..."
            & wails3 build
            Assert-Exit "wails3 build" $LASTEXITCODE
        } finally {
            Pop-Location
        }
        Copy-Item (Join-Path $root "gui\bin\skein-desktop.exe") (Join-Path $Out "skein-desktop.exe") -Force
    }

    if (-not $SkipWintun) {
        Write-Host "Fetching wintun.dll..."
        & (Join-Path $root "scripts\fetch-wintun.cmd") -Dest $Out
        Assert-Exit "fetch-wintun" $LASTEXITCODE
    }
} finally {
    Pop-Location
}

$required = @("skein.exe", "skeind.exe", "skein-helper.exe")
if (-not $SkipDesktop) { $required += "skein-desktop.exe" }
if (-not $SkipWintun) { $required += "wintun.dll" }
foreach ($name in $required) {
    $path = Join-Path $Out $name
    if (-not (Test-Path $path)) { throw "missing build artifact: $path" }
}
Write-Host "build ok: $Out"
