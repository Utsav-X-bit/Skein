# Skein on Windows

The Windows build ships `skeind.exe`, `skein.exe`, and `wintun.dll`. The DLL
must sit next to `skeind.exe`; the daemon loads it when it creates the TUN
interface.

## Install

1. Extract the archive to a directory (a path with spaces is supported).
2. Open an elevated PowerShell in that directory.
3. Create the configuration:

   ```powershell
   .\skein.exe init
   ```

4. Edit `%APPDATA%\skein\skein.toml` so `overlay_ipv4` is unique per node and
   add `[[peer]]` blocks (or leave `mdns = true` on a multicast-capable LAN).
5. Start the daemon in the foreground:

   ```powershell
   .\skeind.exe run -c "$env:APPDATA\skein\skein.toml"
   ```

6. Or install it as a service:

   ```powershell
   .\skeind.exe install-service -c "$env:APPDATA\skein\skein.toml"
   sc start skein
   ```

## Verify

```powershell
.\skein.exe peers list
.\skein.exe routes
ping <remote-overlay-address>
```

The smoke test (`test/windows/smoke.ps1`) automates two local daemons with
distinct interface names and ports, checks the routes, pings across the
overlay, stops both, and verifies cleanup.

## Notes

- Administrator rights are required for TUN creation and route changes.
- The service runs with the same configuration file; keep it owner-only.
- `wintun.dll` is pinned by version and checked against its SHA-256 at
  packaging time.
