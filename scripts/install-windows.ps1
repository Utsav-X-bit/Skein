#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Universal Skein installer for Windows: binaries, service, firewall.

.DESCRIPTION
    Installs (or upgrades) Skein on Windows in one step:

      1. Copies skeind.exe, skein-helper.exe, skein.exe, wintun.dll,
         wintun-LICENSE.txt (and skein-desktop.exe when present) into
         the install directory.
      2. Creates the exact Windows Firewall rules the daemon itself
         manages: "Skein underlay" (inbound UDP on the underlay port)
         plus "Skein overlay" / "Skein overlay out" (and the v6 pair),
         scoped to the overlay prefixes.
      3. Registers the "skein" service through the minimal helper
         (skein-helper install) and verifies it with skein-helper status.

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

    If you built from source, build first (see README Installation,
    Method 2) and run this script from the repository root, or pass
    -SourceDir.

.PARAMETER InstallDir
    Destination directory. Default: C:\Program Files\Skein. Keep the
    default for a first install: bootstrap resolves skeind from the
    package directory (%ProgramFiles%\Skein). An upgrade may use another
    directory under %ProgramFiles%; the helper rejects a daemon outside
    the admin-only Program Files trees.

.PARAMETER SourceDir
    Directory holding the built binaries. Default: the repo's dist/
    next to this script (scripts\..\dist).

.PARAMETER DesktopPath
    Optional explicit path to skein-desktop.exe. Default:
    skein-desktop.exe in SourceDir when present, else the repo's
    gui\bin\skein-desktop.exe next to this script.

.PARAMETER Port
    Underlay UDP/TCP port. Default: 11010 (the daemon default).

.PARAMETER V4Prefix
    Overlay IPv4 prefix the firewall rules are scoped to.
    Default: 10.144.144.0/24 (the daemon default).

.PARAMETER V6Prefix
    Overlay IPv6 prefix the firewall rules are scoped to.
    Default: fd00:6d65:7368::/64 (the daemon default). Empty disables
    the v6 rule pair.

.PARAMETER SkipFirewall
    Do not create firewall rules (the daemon creates them at startup
    anyway; use this only if firewall policy is managed elsewhere).

.PARAMETER SkipService
    Copy binaries and firewall rules only; do not register the service.
    A running service is stopped first so its binaries can be replaced,
    and is left stopped.

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
    [int]$Port = 11010,
    [string]$V4Prefix = "10.144.144.0/24",
    [string]$V6Prefix = "fd00:6d65:7368::/64",
    [switch]$SkipFirewall,
    [switch]$SkipService
)

$ErrorActionPreference = "Stop"

# The helper validates an absolute daemon path, so resolve a relative
# -InstallDir before copying or registering anything.
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)

function Fail([string]$Message) {
    Write-Error $Message
    exit 1
}

# --- 1. Administrator -------------------------------------------------------
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail "Run this script from an elevated PowerShell (Run as administrator)."
}

# --- 2. Locate sources ------------------------------------------------------
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

if ($DesktopPath -eq "") {
    foreach ($candidate in @(
        (Join-Path $SourceDir "skein-desktop.exe"),
        (Join-Path $PSScriptRoot "..\gui\bin\skein-desktop.exe")
    )) {
        if (Test-Path $candidate) { $DesktopPath = $candidate; break }
    }
} elseif (-not (Test-Path $DesktopPath)) {
    Fail "Desktop binary not found at $DesktopPath. Build it first, or omit -DesktopPath to auto-detect it."
}

# --- 3. Stop the service, and refuse a running desktop ----------------------
# A running skein-desktop.exe locks its own exe, so the Copy-Item loop
# below would fail halfway (after the service was already stopped) with
# "The process cannot access the file ... because it is being used by
# another process". Fail before any mutation and let the user close it;
# the desktop is never killed from here. Same tasklist probe as the NSIS
# installer, and it fails closed: a tasklist that cannot run is not
# evidence that the desktop is closed.
$desktopProbe = & "$env:SystemRoot\System32\tasklist.exe" /FI "IMAGENAME eq skein-desktop.exe" /NH /FO CSV 2>$null
if ($LASTEXITCODE -ne 0) {
    Fail "Could not check whether Skein Desktop is running (tasklist exit $LASTEXITCODE). Close Skein Desktop and run this script again; nothing was changed."
}
if ($desktopProbe -match '"skein-desktop\.exe"') {
    Fail "Skein Desktop is running. Close it (quit it from its window, or right-click its taskbar entry and choose Close), then run this script again; nothing was changed."
}

$service = Get-Service -Name "skein" -ErrorAction SilentlyContinue
if ($service -and $service.Status -eq "Running") {
    Write-Host "Stopping running skein service..."
    Stop-Service -Name "skein" -Force
    $service.WaitForStatus("Stopped", (New-TimeSpan -Seconds 30))
}

# --- 4. Copy binaries -------------------------------------------------------
New-Item -ItemType Directory -Force $InstallDir | Out-Null
foreach ($file in $required) {
    Copy-Item (Join-Path $SourceDir $file) $InstallDir -Force
}
if ($DesktopPath) {
    Copy-Item $DesktopPath $InstallDir -Force
    Write-Host "Installed skein-desktop.exe"
} else {
    Write-Host "Note: skein-desktop.exe not found; CLI/service install only."
}
Write-Host "Binaries installed to $InstallDir"

# --- 5. Firewall rules (exactly the daemon's own rule set) ------------------
if (-not $SkipFirewall) {
    $underlay = "Skein underlay"
    $existing = netsh advfirewall firewall show rule name="$underlay" 2>$null
    if ($existing -match "Rule Name:\s+$underlay") {
        Write-Host "Firewall rule '$underlay' already exists, keeping it."
    } else {
        netsh advfirewall firewall add rule name="$underlay" dir=in action=allow protocol=UDP localport=$Port profile=any | Out-Null
        if ($LASTEXITCODE -ne 0) { Fail "Failed to create firewall rule '$underlay'." }
        Write-Host "Created firewall rule '$underlay' (inbound UDP $Port)."
    }

    $overlayRules = @(
        @{ Name = "Skein overlay";     Direction = "Inbound";  Prefix = $V4Prefix },
        @{ Name = "Skein overlay out"; Direction = "Outbound"; Prefix = $V4Prefix }
    )
    if ($V6Prefix -ne "") {
        $overlayRules += @(
            @{ Name = "Skein overlay v6";     Direction = "Inbound";  Prefix = $V6Prefix },
            @{ Name = "Skein overlay out v6"; Direction = "Outbound"; Prefix = $V6Prefix }
        )
    }
    foreach ($rule in $overlayRules) {
        if (Get-NetFirewallRule -DisplayName $rule.Name -ErrorAction SilentlyContinue) {
            Write-Host "Firewall rule '$($rule.Name)' already exists, keeping it."
        } else {
            New-NetFirewallRule -DisplayName $rule.Name -Direction $rule.Direction -Action Allow -RemoteAddress $rule.Prefix -Profile Any -ErrorAction Stop | Out-Null
            Write-Host "Created firewall rule '$($rule.Name)' ($($rule.Direction), $($rule.Prefix))."
        }
    }
} else {
    Write-Host "Skipping firewall rules (-SkipFirewall); the daemon creates them at startup."
}

# --- 6. Service registration through the minimal helper ---------------------
# helper install needs an existing system config ("no system config:
# run init or import first" on a fresh install) — the service is
# registered by the bootstrap flow when the first network is created,
# so on a fresh install this step is skipped, not failed.
if (-not $SkipService) {
    $helper = Join-Path $InstallDir "skein-helper.exe"
    $systemConfig = Join-Path $env:ProgramData "Skein\skein.toml"
    $hadConfig = Test-Path $systemConfig
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
    $hadConfig = Test-Path (Join-Path $env:ProgramData "Skein\skein.toml")
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
Write-Host "Your config in %ProgramData%\Skein is kept by reinstalls."
