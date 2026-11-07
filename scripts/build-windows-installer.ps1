#Requires -Version 5.1
<#
.SYNOPSIS
    Build the complete Skein setup executable on Windows 10/11.

.DESCRIPTION
    Locates Go, Node.js, Wails v3.1.0 and NSIS even when their installers did
    not update the current shell's PATH, then runs the repository's packaging
    task and verifies the artifact. It never installs Skein and never starts
    the service.

    Missing build tools can be installed with -InstallPrerequisites (winget
    for Go/Node/NSIS, then the pinned Wails CLI). That changes this machine's
    development tooling only, not Skein.

.PARAMETER Arch
    Target architecture: amd64 (default) or arm64.

.PARAMETER InstallPrerequisites
    Install missing Go, Node.js LTS, NSIS and Wails v3.1.0 with winget/go.

.PARAMETER RunInstaller
    Start the built installer after a successful build (UAC prompt).

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\build-windows-installer.ps1

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\build-windows-installer.ps1 -InstallPrerequisites
#>
[CmdletBinding()]
param(
    [ValidateSet("amd64", "arm64")]
    [string]$Arch = "amd64",
    [switch]$InstallPrerequisites,
    [switch]$RunInstaller
)

# Native tools (go, wails3, winget) write normal output to stderr; "Stop"
# turns that into a terminating NativeCommandError. Check exit codes instead.
$ErrorActionPreference = "Continue"

function Fail([string]$Message) {
    Write-Error $Message
    exit 1
}

function Refresh-Path {
    $machine = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $user = [Environment]::GetEnvironmentVariable("Path", "User")
    $goBin = Join-Path $env:USERPROFILE "go\bin"
    $env:PATH = "$machine;$user;$goBin"
}

function Find-Exe([string]$Name, [string[]]$Candidates) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    foreach ($candidate in $Candidates) {
        if ($candidate -and (Test-Path -LiteralPath $candidate)) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    return $null
}

function Install-WingetPackage([string]$Id) {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        Fail "winget is unavailable. Install $Id manually, then rerun without -InstallPrerequisites."
    }
    Write-Host "Installing $Id with winget..."
    & winget install --id $Id -e --source winget --accept-package-agreements --accept-source-agreements
    if ($LASTEXITCODE -ne 0) { Fail "winget failed to install $Id (exit $LASTEXITCODE)." }
    Refresh-Path
}

if (-not [Environment]::Is64BitOperatingSystem) {
    Fail "Skein's setup is 64-bit. Use 64-bit Windows 10 or 11."
}
if ([Environment]::OSVersion.Version.Major -lt 10) {
    Fail "Skein requires Windows 10 or later."
}

$root = Split-Path $PSScriptRoot -Parent
if (-not (Test-Path (Join-Path $root "go.mod"))) {
    Fail "This script must stay inside the Skein repository (scripts\build-windows-installer.ps1)."
}

$goCandidates = @(
    (Join-Path $env:ProgramFiles "Go\bin\go.exe"),
    (Join-Path $env:LOCALAPPDATA "Programs\Go\bin\go.exe")
)
$nodeCandidates = @((Join-Path $env:ProgramFiles "nodejs\node.exe"))
$wailsCandidates = @(
    (Join-Path $env:USERPROFILE "go\bin\wails3.exe"),
    (Join-Path (Join-Path $env:USERPROFILE "go") "bin\wails3.exe")
)
$nsisCandidates = @(
    (Join-Path ${env:ProgramFiles(x86)} "NSIS\makensis.exe"),
    (Join-Path $env:ProgramFiles "NSIS\makensis.exe")
)

# --- prerequisites ---------------------------------------------------------
$go = Find-Exe "go" $goCandidates
if (-not $go -and $InstallPrerequisites) { Install-WingetPackage "GoLang.Go"; $go = Find-Exe "go" $goCandidates }
if (-not $go) { Fail "Go was not found. Install Go 1.26+ (winget install GoLang.Go), reopen PowerShell, and rerun; or use -InstallPrerequisites." }

$goVersion = (& $go version 2>&1 | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or -not $goVersion) { Fail "Go was found at $go but did not run." }
if ($goVersion -notmatch "go1\.(2[6-9]|[3-9][0-9])") {
    Write-Warning "$goVersion is older than the go.mod requirement (1.26.10). Go may download the required toolchain automatically."
}

$node = Find-Exe "node" $nodeCandidates
if (-not $node -and $InstallPrerequisites) { Install-WingetPackage "OpenJS.NodeJS.LTS"; $node = Find-Exe "node" $nodeCandidates }
if (-not $node) { Fail "Node.js was not found. Install Node.js 22+ (winget install OpenJS.NodeJS.LTS), reopen PowerShell, and rerun; or use -InstallPrerequisites." }
$nodeMajor = 0
[void][int]::TryParse(((& $node -p "process.versions.node.split('.')[0]" 2>&1 | Out-String).Trim()), [ref]$nodeMajor)
if ($nodeMajor -lt 22) {
    Fail "Node.js $nodeMajor is too old; Node.js 22+ is required. Install a newer LTS and rerun."
}

$wails = Find-Exe "wails3" $wailsCandidates
if (-not $wails -and $InstallPrerequisites) {
    Write-Host "Installing pinned Wails v3.1.0..."
    & $go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
    if ($LASTEXITCODE -ne 0) { Fail "Installing Wails v3.1.0 failed (exit $LASTEXITCODE)." }
    Refresh-Path
    $wails = Find-Exe "wails3" $wailsCandidates
}
if (-not $wails) {
    Fail "wails3 was not found. Run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0; then add `$(go env GOPATH)\bin to PATH; or use -InstallPrerequisites."
}
$wailsVersion = (& $wails version 2>&1 | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $wailsVersion -notmatch "v3\.1\.0") {
    if (-not $InstallPrerequisites) {
        Fail "wails3 at $wails is not v3.1.0 ($wailsVersion). Install the pinned version or use -InstallPrerequisites."
    }
    & $go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
    if ($LASTEXITCODE -ne 0) { Fail "Installing Wails v3.1.0 failed (exit $LASTEXITCODE)." }
}

$makensis = Find-Exe "makensis" $nsisCandidates
if (-not $makensis -and $InstallPrerequisites) { Install-WingetPackage "NSIS.NSIS"; $makensis = Find-Exe "makensis" $nsisCandidates }
if (-not $makensis) {
    Fail "makensis was not found. Install NSIS 3.x (winget install NSIS.NSIS) and rerun; the usual path is `"$($nsisCandidates[0])`"."
}

# The current shell may predate the installers; use the resolved directories.
$env:PATH = "$(Split-Path $makensis);$(Split-Path $go);$(Split-Path $node);$(Split-Path $wails);$env:PATH"
$env:npm_config_audit = "false"
$env:npm_config_fund = "false"

Write-Host "Go:      $go ($goVersion)"
Write-Host "Node:    $node (v$nodeMajor)"
Write-Host "Wails:   $wails"
Write-Host "NSIS:    $makensis"

# --- build -----------------------------------------------------------------
$gui = Join-Path $root "gui"
$bin = Join-Path $gui "bin"

# An interrupted build can leave a stale executable that `go build -o` refuses
# to replace. Clear only this checkout's outputs, and refuse while one of them
# is running (a locked exe cannot be replaced).
$running = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
    Where-Object {
        $_.Name -in @("skeind.exe", "skein.exe", "skein-helper.exe", "skein-desktop.exe") -and
        $_.ExecutablePath -and
        $_.ExecutablePath.StartsWith($bin, [StringComparison]::OrdinalIgnoreCase)
    })
if ($running.Count -gt 0) {
    Fail "A Skein process is running from $bin. Quit the desktop and stop the service, then rerun."
}
foreach ($name in @("skeind.exe", "skein.exe", "skein-helper.exe", "skein-desktop.exe")) {
    $path = Join-Path $bin $name
    Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath "$path~" -Force -ErrorAction SilentlyContinue
}

Push-Location $gui
try {
    & $wails doctor
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "wails3 doctor reported issues; the build may still succeed."
    }
    Write-Host "Building the complete Windows setup..."
    & $wails task windows:create:nsis:installer "ARCH=$Arch" "MAKENSIS=$makensis"
    if ($LASTEXITCODE -ne 0) {
        Fail "The packaging task failed (exit $LASTEXITCODE). The error above names the missing prerequisite or file."
    }
} finally {
    Pop-Location
}

$artifact = Join-Path $gui "bin\skein-desktop-$Arch-installer.exe"
if (-not (Test-Path -LiteralPath $artifact)) {
    Fail "The build finished but $artifact is missing."
}
$sizeMb = [math]::Round((Get-Item -LiteralPath $artifact).Length / 1MB, 1)
Write-Host ""
Write-Host "Setup executable: $artifact ($sizeMb MB)"
Write-Host "Run it as Administrator, then launch Skein Desktop and create or join a network."

if ($RunInstaller) {
    Start-Process -FilePath $artifact
}
