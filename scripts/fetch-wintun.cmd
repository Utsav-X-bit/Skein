@echo off
rem Run fetch-wintun.ps1 regardless of the PowerShell execution policy.
rem
rem   scripts\fetch-wintun.cmd [-Arch amd64] [-Dest dist]
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0fetch-wintun.ps1" %*
exit /b %ERRORLEVEL%
