# Launch the desktop app, verify its window and single-instance behavior,
# then close it. The GUI is started with SKEIN_NO_TRAY=1 so closing the
# window must exit the process.

param([string]$Bin = "")

. (Join-Path $PSScriptRoot "common.ps1")
$Bin = Resolve-Bin $Bin
$gui = Join-Path $Bin "skein-desktop.exe"
if (-not (Test-Path $gui)) { throw "skein-desktop.exe not found in $Bin" }

$oldNoTray = $env:SKEIN_NO_TRAY
$env:SKEIN_NO_TRAY = "1"
$proc = $null
try {
    $proc = Start-Process -FilePath $gui -PassThru
    $deadline = (Get-Date).AddSeconds(20)
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 250
        $proc.Refresh()
        if ($proc.HasExited) { throw "skein-desktop exited during startup" }
        if ($proc.MainWindowHandle -ne 0) { break }
    }
    $proc.Refresh()
    if ($proc.MainWindowHandle -eq 0) { throw "skein-desktop did not create a window" }
    Write-Host "desktop window: $($proc.MainWindowTitle)"

    $second = Start-Process -FilePath $gui -PassThru -Wait
    if ($second.ExitCode -ne 0) { throw "second launch exited $($second.ExitCode), want 0" }
    $proc.Refresh()
    if ($proc.HasExited) { throw "second launch terminated the running instance" }
    Write-Host "single-instance launch passed"

    $proc.CloseMainWindow() | Out-Null
    if (-not $proc.WaitForExit(10000)) {
        throw "skein-desktop did not exit after closing its window"
    }
    Write-Host "desktop close passed"
} finally {
    if ($proc -and -not $proc.HasExited) {
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    }
    if ($null -eq $oldNoTray) { Remove-Item Env:\SKEIN_NO_TRAY -ErrorAction SilentlyContinue }
    else { $env:SKEIN_NO_TRAY = $oldNoTray }
}
