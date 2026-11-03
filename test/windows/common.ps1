# Shared helpers for the Windows test scripts.

$ErrorActionPreference = "Stop"

function Assert-Admin {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "this test must run from an elevated PowerShell (Run as Administrator)"
    }
}

function Resolve-Bin {
    param([string]$Bin = "")
    if (-not $Bin) {
        $candidate = Join-Path $PSScriptRoot "..\..\dist"
        if (Test-Path (Join-Path $candidate "skein.exe")) {
            $Bin = $candidate
        } else {
            $Bin = (Get-Location).Path
        }
    }
    if (-not (Test-Path (Join-Path $Bin "skein.exe"))) {
        throw "skein.exe not found in $Bin; pass -Bin <directory>"
    }
    return (Resolve-Path $Bin).Path
}

function Assert-Exit {
    param([string]$What, [int]$Code)
    if ($Code -ne 0) { throw "$What failed with exit code $Code" }
}

function Invoke-Native {
    param([string]$File, [string[]]$Arguments = @())
    $output = & $File @Arguments 2>&1
    return @{ Output = ($output -join "`n"); Code = $LASTEXITCODE }
}

function Get-ServiceState {
    $svc = Get-Service -Name skein -ErrorAction SilentlyContinue
    if (-not $svc) { return "missing" }
    return [string]$svc.Status
}
