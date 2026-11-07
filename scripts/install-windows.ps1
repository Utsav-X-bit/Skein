#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Universal Skein installer for Windows 10/11 x64: binaries, service.

.DESCRIPTION
    Installs (or upgrades) Skein on Windows in one step:

      1. Checks the machine prerequisites: Windows 10 or later, a
         64-bit process, the machine-wide Microsoft Edge WebView2
         Runtime, and a destination under %ProgramFiles%.
      2. Copies skeind.exe, skein-helper.exe, skein.exe, wintun.dll,
         wintun-LICENSE.txt and skein-desktop.exe into the install
         directory, then verifies each file landed.
      3. Registers the "skein" service through the minimal helper
         (skein-helper install --daemon <installed skeind>) and
         verifies it with skein-helper status.

    Firewall rules are NOT created here. The daemon owns the rule
    set and (re)creates it from the configured underlay port and
    overlay prefixes when a network is created/started, so a
    pre-install script cannot guess the port or clobber unrelated
    rules.

    The script does not delete or reset %ProgramData%\Skein, so
    reinstalling or upgrading keeps your config, identity, and network
    membership. Uninstall is the helper's job (skein-helper uninstall);
    purge is the daemon's reset endpoint, not this script.

    On a first install there is no network yet, so the service is
    not registered here — it is registered by the bootstrap flow
    when you create or join a network (helper install needs an
    existing config and fails without one). On an upgrade the service
    is re-registered and started, so your network comes back up.

    First install, after this script finishes (run these in an elevated
    PowerShell; the helper mints a single-use bootstrap token and the
    daemon writes the system config as the single writer):

        .\skein.exe system init [--name NAME] [--port PORT]
        .\skein.exe system join "skein:2:…"

    Or launch the installed skein-desktop.exe with your normal
    (non-elevated) token and use its onboarding to create or join.

    If you built from source, build first (see README Installation,
    Method 2) and run this script from the repository root, or pass
    -SourceDir.

.PARAMETER InstallDir
    Destination directory. Default: C:\Program Files\Skein. It MUST be
    a subdirectory of %ProgramFiles%: the daemon and helper trust only
    admin-only parents, and the complete package is machine-wide. A
    first install must use the default canonical directory (the
    desktop's first-setup bootstrap searches it); a custom subdirectory
    is accepted only when upgrading an existing configured install,
    where the helper is registered with --daemon <exact path>.

.PARAMETER SourceDir
    Directory holding the built binaries. Default: the repo's dist/
    next to this script (scripts\..\dist).

.PARAMETER DesktopPath
    Explicit path to skein-desktop.exe. Default:
    skein-desktop.exe in SourceDir when present, else the repo's
    gui\bin\skein-desktop.exe next to this script. A complete install
    requires the desktop binary; the script fails rather than silently
    installing a CLI/service-only package.

.PARAMETER SkipService
    Copy binaries only; do not register the service. A running service
    is stopped first so its binaries can be replaced, and is left
    stopped.

.EXAMPLE
    powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install-windows.ps1

.EXAMPLE
    powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\install-windows.ps1 -SourceDir C:\build\dist
#>
[CmdletBinding()]
param(
    [string]$InstallDir = "$env:ProgramFiles\Skein",
    [string]$SourceDir = "",
    [string]$DesktopPath = "",
    [switch]$SkipService
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    Write-Error $Message
    exit 1
}

# --- 1. Administrator, platform, and destination ---------------------------
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail "Run this script from an elevated PowerShell (Run as administrator)."
}
if (-not [Environment]::Is64BitOperatingSystem -or -not [Environment]::Is64BitProcess) {
    Fail "Skein is a 64-bit Windows 10/11 package. Run this script from the 64-bit PowerShell (not the x86 one)."
}
if ([Environment]::OSVersion.Version.Major -lt 10) {
    Fail "Skein requires Windows 10 or later."
}

# The helper validates an absolute daemon path, so resolve a relative
# -InstallDir before copying or registering anything. It must be a
# strict subdirectory of %ProgramFiles%: the daemon and helper accept
# only admin-only parents, and a complete VPN package is machine-wide.
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir).TrimEnd([System.IO.Path]::DirectorySeparatorChar)
$programFiles = [System.IO.Path]::GetFullPath($env:ProgramFiles).TrimEnd([System.IO.Path]::DirectorySeparatorChar)
if (-not $InstallDir.StartsWith($programFiles + '\', [System.StringComparison]::OrdinalIgnoreCase)) {
    Fail "InstallDir must be a subdirectory of $programFiles (for example $programFiles\Skein); got $InstallDir. The VPN service runs as SYSTEM and the binaries must be admin-only."
}

# The desktop/CLI first-setup bootstrap registers the daemon through
# the package search (%ProgramFiles%\Skein), so a first install must use
# the canonical directory. A custom subdirectory is safe only on an
# upgrade with an existing config, where helper install --daemon pins
# the exact installed path.
$systemConfig = Join-Path $env:ProgramData "Skein\skein.toml"
$hadConfig = Test-Path $systemConfig
$canonicalInstallDir = Join-Path $programFiles "Skein"
if (-not $hadConfig -and -not $InstallDir.Equals($canonicalInstallDir, [System.StringComparison]::OrdinalIgnoreCase)) {
    Fail "On a first install, use $canonicalInstallDir: the desktop's first-setup flow starts the daemon from the standard package directory. A custom subdirectory is supported only when upgrading an existing configured install. Nothing was changed."
}

# --- 2. Machine-wide WebView2 Runtime --------------------------------------
# The desktop is a WebView2 app; the source install does not download
# prerequisites, so check the machine-wide runtime up front instead of
# installing a desktop that cannot start.
$webView2Keys = @(
    "HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
    "HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
)
$webView2Version = $null
foreach ($key in $webView2Keys) {
    $pv = (Get-ItemProperty -Path $key -Name pv -ErrorAction SilentlyContinue).pv
    if ($pv) { $webView2Version = $pv; break }
}
if (-not $webView2Version) {
    Fail "The machine-wide Microsoft Edge WebView2 Runtime is not installed. Install the Evergreen WebView2 Runtime, then run this script again; nothing was changed."
}

# --- 3. Locate sources ------------------------------------------------------
if ($SourceDir -eq "") {
    $SourceDir = Join-Path $PSScriptRoot "..\dist"
}
$SourceDir = (Resolve-Path $SourceDir -ErrorAction SilentlyContinue).Path
if (-not $SourceDir) {
    Fail "Source directory not found. Build first (see README Installation, Method 2) or pass -SourceDir."
}

# wintun-LICENSE.txt ships beside the DLL: the fetch scripts install both,
# and the NSIS installer ships both, so the source install must too.
$required = @("skeind.exe", "skein-helper.exe", "skein.exe", "wintun.dll", "wintun-LICENSE.txt")
foreach ($file in $required) {
    if (-not (Test-Path (Join-Path $SourceDir $file))) {
        Fail "Missing $file in $SourceDir. Build first, and fetch wintun.dll and its licence with scripts\fetch-wintun.cmd -Dest `"$SourceDir`"."
    }
}

# A complete install includes the desktop. Auto-detect it, but fail
# rather than silently producing a CLI/service-only package.
if ($DesktopPath -eq "") {
    foreach ($candidate in @(
        (Join-Path $SourceDir "skein-desktop.exe"),
        (Join-Path $PSScriptRoot "..\gui\bin\skein-desktop.exe")
    )) {
        if (Test-Path $candidate) { $DesktopPath = $candidate; break }
    }
}
if (-not $DesktopPath) {
    Fail "skein-desktop.exe not found in $SourceDir or gui\bin. Build the desktop first, or pass -DesktopPath. A complete Skein install includes it."
}
if (-not (Test-Path $DesktopPath)) {
    Fail "Desktop binary not found at $DesktopPath. Build it first, or omit -DesktopPath to auto-detect it."
}

# --- 4. Stop the service, and refuse a running desktop ----------------------
# A running skein-desktop.exe locks its own exe, so the Copy-Item loop
# below would fail halfway (after the service was already stopped) with
# "The process cannot access the file ... because it is being used by
# another process". Fail before any mutation and let the user quit it
# from the system-tray menu (the window Close button only hides it to
# the tray and leaves the process running); the desktop is never killed
# from here. Same tasklist probe as the NSIS installer, and it fails
# closed: a tasklist that cannot run is not evidence that the desktop
# is closed.
$desktopProbe = & "$env:SystemRoot\System32\tasklist.exe" /FI "IMAGENAME eq skein-desktop.exe" /NH /FO CSV 2>$null
if ($LASTEXITCODE -ne 0) {
    Fail "Could not check whether Skein Desktop is running (tasklist exit $LASTEXITCODE). Quit Skein Desktop from its system-tray menu and run this script again; nothing was changed."
}
if ($desktopProbe -match '"skein-desktop\.exe"') {
    Fail "Skein Desktop is running. Quit it from its system-tray menu, then run this script again; nothing was changed."
}

# A service that refuses to stop must abort BEFORE the copy: replacing
# binaries around a running daemon leaves mismatched files and locked
# destinations. Stop-Service/ WaitForStatus are terminating here, so a
# timeout fails the install instead of falling through.
$service = Get-Service -Name "skein" -ErrorAction SilentlyContinue
if ($service -and $service.Status -ne "Stopped") {
    Write-Host "Stopping running skein service..."
    try {
        Stop-Service -Name "skein" -Force -ErrorAction Stop
        $service.WaitForStatus("Stopped", (New-TimeSpan -Seconds 30))
    } catch {
        Fail "Could not stop the running skein service ($($_.Exception.Message)); no files were replaced. Stop it manually (sc.exe stop skein) and run this script again."
    }
}

# --- 5. Copy binaries -------------------------------------------------------
New-Item -ItemType Directory -Force $InstallDir | Out-Null
foreach ($file in $required) {
    Copy-Item (Join-Path $SourceDir $file) $InstallDir -Force
}
Copy-Item $DesktopPath (Join-Path $InstallDir "skein-desktop.exe") -Force
Write-Host "Installed skein-desktop.exe"

# Verify every file landed. A partial copy is an install failure, not
# a success; nothing is registered below until this passes.
foreach ($file in ($required + @("skein-desktop.exe"))) {
    $dst = Join-Path $InstallDir $file
    if (-not (Test-Path $dst)) {
        Fail "Copy verification failed: $dst is missing. Nothing was registered."
    }
    if ((Get-Item $dst).Length -eq 0) {
        Fail "Copy verification failed: $dst is empty. Nothing was registered."
    }
}
Write-Host "Binaries installed to $InstallDir (verified)."

# Firewall rules are owned by the daemon: it (re)creates the underlay
# and overlay rules from the configured port and prefixes when a
# network is created or started. Creating them here would guess the
# port and could clobber unrelated rules, so this script does not.
Write-Host "Firewall rules will be created by the daemon when a network connects."

# --- 6. Service registration through the minimal helper ---------------------
# helper install needs an existing system config ("no system config:
# run init or import first" on a fresh install) — the service is
# registered by the bootstrap flow when the first network is created,
# so on a fresh install this step is skipped, not failed.
if (-not $SkipService) {
    $helper = Join-Path $InstallDir "skein-helper.exe"
    if ($hadConfig) {
        Write-Host "Registering the skein service..."
        # --daemon pins registration to this script's install directory.
        # Without it the helper searches only %ProgramFiles%\Skein, which
        # is wrong for a custom -InstallDir.
        & $helper install --daemon (Join-Path $InstallDir "skeind.exe")
        if ($LASTEXITCODE -ne 0) { Fail "skein-helper install failed (exit $LASTEXITCODE)." }

        Write-Host "Verifying service status..."
        & $helper status
        if ($LASTEXITCODE -ne 0) { Fail "skein-helper status failed (exit $LASTEXITCODE)." }
    } else {
        Write-Host "No network configured yet; the service will be registered when you create or join a network."
    }
} else {
    Write-Host "Skipping service registration (-SkipService)."
}

Write-Host ""
if ($hadConfig -and -not $SkipService) {
    Write-Host "Skein installed and the service is running. Your network is back up."
} elseif ($hadConfig) {
    # -SkipService stopped the old service to replace its binaries but did
    # not re-register or start it; say so instead of claiming it is running.
    Write-Host "Skein installed; service registration was skipped (-SkipService). Start the existing service with:"
    Write-Host "  & `"$InstallDir\skein-helper.exe`" start"
} else {
    Write-Host "Skein installed. Next steps (run these in an elevated PowerShell, from $InstallDir):"
    Write-Host "  First node:  .\skein.exe system init [--name NAME]"
    Write-Host "  Joining node: .\skein.exe system join `"skein:2:…`""
}
Write-Host "Launch `"$InstallDir\skein-desktop.exe`" with your normal (non-elevated) token to create or join a network."
Write-Host "Your config in %ProgramData%\Skein is kept by reinstalls."
