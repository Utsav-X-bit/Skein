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
    for Go/Node/NSIS, then the pinned Wails CLI). A too-old Node.js or
    wails3 is upgraded too, not just reported. That changes this machine's
    development tooling only, not Skein.

.PARAMETER Arch
    Target architecture: amd64 (default) or arm64.

.PARAMETER InstallPrerequisites
    Install missing Go, Node.js 22.12+, NSIS and Wails v3.1.0 with
    winget/go, and upgrade an existing Node.js or wails3 that is too old.

.PARAMETER RunInstaller
    Start the built setup executable after a successful build. The setup
    prompts for UAC itself; this script never installs Skein for you.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\build-windows-installer.ps1

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\build-windows-installer.ps1 -InstallPrerequisites

.EXAMPLE
    powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-windows-installer.ps1 -InstallPrerequisites -RunInstaller
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

function Install-WingetPackage([string]$Id, [switch]$Upgrade) {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        # winget is absent on some Windows 10 LTSC/Server images and on
        # machines where App Installer was removed. Name every manual
        # step instead of sending the user after one package ID.
        Fail @"
winget is unavailable, so -InstallPrerequisites cannot install $Id.
Install the build tools by hand, reopen PowerShell, and rerun this script
without -InstallPrerequisites:
  Go 1.26+        https://go.dev/dl/
  Node.js 22.12+  https://nodejs.org/en/download
  NSIS 3.x        https://nsis.sourceforge.io/Download
  Wails v3.1.0    go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
"@
    }
    if ($Upgrade) {
        # An already-installed, too-old copy: winget install would refuse
        # with "already installed", so ask for the upgrade explicitly and
        # fall back to install when the old copy is not winget-managed.
        Write-Host "Upgrading $Id with winget..."
        & winget upgrade --id $Id -e --source winget --accept-package-agreements --accept-source-agreements
        if ($LASTEXITCODE -eq 0) { Refresh-Path; return }
        Write-Host "winget upgrade could not update $Id (exit $LASTEXITCODE); installing the current package instead."
    }
    Write-Host "Installing $Id with winget..."
    & winget install --id $Id -e --source winget --accept-package-agreements --accept-source-agreements
    if ($LASTEXITCODE -ne 0) {
        if ($Upgrade) {
            # Both attempts failed; the caller re-reads the version and
            # reports the exact stale version instead of a winget code.
            Write-Warning "winget could not update $Id (exit $LASTEXITCODE)."
            return
        }
        Fail "winget failed to install $Id (exit $LASTEXITCODE)."
    }
    Refresh-Path
}

function Install-PinnedWails([string]$Go) {
    Write-Host "Installing pinned Wails v3.1.0..."
    & $Go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
    if ($LASTEXITCODE -ne 0) { Fail "Installing Wails v3.1.0 failed (exit $LASTEXITCODE)." }
    $goBin = (& $Go env GOBIN 2>&1 | Out-String).Trim()
    if (-not $goBin) {
        $goPath = (& $Go env GOPATH 2>&1 | Out-String).Trim()
        # GOPATH may be a ';'-separated list; go install uses the first.
        $goBin = Join-Path ($goPath -split ';')[0] "bin"
    }
    $exe = Join-Path $goBin "wails3.exe"
    if (-not (Test-Path -LiteralPath $exe)) {
        Fail "go install succeeded but $exe is missing; add $(Split-Path $exe) to PATH and rerun."
    }
    # Put the pinned binary ahead of any older wails3 earlier on PATH.
    $env:PATH = "$goBin;$env:PATH"
    return $exe
}

if (-not [Environment]::Is64BitOperatingSystem) {
    Fail "Skein's setup is 64-bit. Use 64-bit Windows 10 or 11."
}
if (-not [Environment]::Is64BitProcess) {
    Fail "Run this from the 64-bit PowerShell: the setup is 64-bit and Go/Node/NSIS install into the 64-bit locations."
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
if (-not $node) { Fail "Node.js was not found. Install Node.js 22.12+ (winget install OpenJS.NodeJS.LTS), reopen PowerShell, and rerun; or use -InstallPrerequisites." }

# Vite 8's package-lock engines field is "^20.19.0 || >=22.12.0", so a
# major-only check lets Node 22.0-22.11 through and then `vite build`
# fails on the engine range.
$nodeVersion = [version]"0.0"
$nodeRaw = (& $node -p "process.versions.node" 2>&1 | Out-String).Trim()
if (-not [version]::TryParse($nodeRaw, [ref]$nodeVersion)) {
    Fail "Could not read the Node.js version from $node (got '$nodeRaw')."
}
if ($nodeVersion -lt [version]"22.12.0") {
    if (-not $InstallPrerequisites) {
        Fail "Node.js $nodeRaw is too old: the desktop's Vite build requires Node.js 22.12+ (or a newer major). Install a newer LTS, reopen PowerShell, and rerun; or use -InstallPrerequisites."
    }
    Install-WingetPackage "OpenJS.NodeJS.LTS" -Upgrade
    $node = Find-Exe "node" $nodeCandidates
    if (-not $node) {
        Fail "Node.js was not found after -InstallPrerequisites; install Node.js 22.12+ from https://nodejs.org/en/download, reopen PowerShell, and rerun."
    }
    $nodeRaw = (& $node -p "process.versions.node" 2>&1 | Out-String).Trim()
    if (-not [version]::TryParse($nodeRaw, [ref]$nodeVersion) -or $nodeVersion -lt [version]"22.12.0") {
        Fail "Node.js is still $nodeRaw after -InstallPrerequisites; Node.js 22.12+ is required. Install it from https://nodejs.org/en/download, reopen PowerShell, and rerun."
    }
}

$wails = Find-Exe "wails3" $wailsCandidates
$wailsVersion = ""
$wailsPinned = $false
if ($wails) {
    $wailsVersion = (& $wails version 2>&1 | Out-String).Trim()
    $wailsPinned = ($LASTEXITCODE -eq 0 -and $wailsVersion -match "v3\.1\.0")
}
if (-not $wails) {
    if (-not $InstallPrerequisites) {
        Fail "wails3 was not found. Run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0; then add `$(go env GOPATH)\bin to PATH; or use -InstallPrerequisites."
    }
    $wails = Install-PinnedWails $go
} elseif (-not $wailsPinned) {
    if (-not $InstallPrerequisites) {
        Fail "wails3 at $wails is not v3.1.0 ($wailsVersion). Install the pinned version or use -InstallPrerequisites."
    }
    # An older wails3 on PATH must actually be replaced, not reported as
    # handled: install the pinned build and use it from GOPATH's bin.
    $wails = Install-PinnedWails $go
    $wailsVersion = (& $wails version 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $wailsVersion -notmatch "v3\.1\.0") {
        Fail "The pinned wails3 at $wails still reports '$wailsVersion'. Remove the older wails3 earlier on PATH and rerun."
    }
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
Write-Host "Node:    $node (v$nodeRaw)"
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
Write-Host "Approve the setup's UAC prompt, then launch Skein Desktop as your normal user and create or join a network."

if ($RunInstaller) {
    Start-Process -FilePath $artifact
}
