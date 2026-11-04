#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Universal Skein installer for Windows: binaries, service, firewall.

.DESCRIPTION
    Installs (or upgrades) Skein on Windows in one step:

      1. Copies skeind.exe, skein-helper.exe, skein.exe, wintun.dll
         (and skein-desktop.exe when present) into the install directory.
      2. Creates the exact Windows Firewall rules the daemon itself
         manages: "Skein underlay" (inbound UDP on the underlay port)
         plus "Skein overlay" / "Skein overlay out" (and the v6 pair),
         scoped to the overlay prefixes.
      3. Registers the "skein" service through the minimal helper
         (skein-helper install) and verifies it with skein-helper status.

    The script never touches %ProgramData%\Skein, so reinstalling or
    upgrading keeps your config, identity, and network membership.
    Uninstall is the helper's job (skein-helper uninstall); purge is
    the daemon's reset endpoint, not this script.

    After this script finishes, create or join a network (the helper
    mints a single-use bootstrap token and the daemon writes the
    system config as the single writer):

        .\skein.exe system init [--name NAME] [--port PORT]
        .\skein.exe system join "skein:2:…"

    If you built from source, build first (see README "Building") and
    run this script from the repository root, or pass -SourceDir.

.PARAMETER InstallDir
    Destination directory. Default: C:\Program Files\Skein.

.PARAMETER SourceDir
    Directory holding the built binaries. Default: the repo's dist/
    next to this script (scripts\..\dist).

.PARAMETER DesktopPath
    Optional explicit path to skein-desktop.exe. Default: the repo's
    gui\bin\skein-desktop.exe next to this script, when it exists.

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
    Fail "Source directory not found. Build first (see README 'Building') or pass -SourceDir."
}

$required = @("skeind.exe", "skein-helper.exe", "skein.exe", "wintun.dll")
foreach ($file in $required) {
    if (-not (Test-Path (Join-Path $SourceDir $file))) {
        Fail "Missing $file in $SourceDir. Build first, and fetch wintun.dll with scripts\fetch-wintun.cmd -Dest dist."
    }
}

if ($DesktopPath -eq "") {
    $candidate = Join-Path $PSScriptRoot "..\gui\bin\skein-desktop.exe"
    if (Test-Path $candidate) { $DesktopPath = $candidate }
}

# --- 3. Stop a running service so binaries can be replaced ------------------
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
if ($DesktopPath -and (Test-Path $DesktopPath)) {
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
if (-not $SkipService) {
    $helper = Join-Path $InstallDir "skein-helper.exe"
    Write-Host "Registering the skein service..."
    & $helper install
    if ($LASTEXITCODE -ne 0) { Fail "skein-helper install failed (exit $LASTEXITCODE)." }

    Write-Host "Verifying service status..."
    & $helper status
    if ($LASTEXITCODE -ne 0) { Fail "skein-helper status failed (exit $LASTEXITCODE)." }
} else {
    Write-Host "Skipping service registration (-SkipService)."
}

Write-Host ""
Write-Host "Skein installed. Next steps (from $InstallDir):"
Write-Host "  First node:  .\skein.exe system init [--name NAME]"
Write-Host "  Joining node: .\skein.exe system join `"skein:2:…`""
Write-Host "  Then:        .\skein.exe system start   (or start the service from Services)"
Write-Host "Your config in %ProgramData%\Skein is untouched by reinstalls."
