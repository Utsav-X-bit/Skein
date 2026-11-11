<p align="center">
  <img src="Logo.png" alt="Skein" width="120" />
</p>

# Skein

**A private mesh for your own devices.**

Skein connects a small group of Linux and Windows machines over an encrypted IPv4/IPv6 overlay. Use their overlay addresses to reach services across your own computers, servers, and home networks.

- **End-to-end encryption:** Noise IKpsk2 sessions between peers; relay nodes forward encrypted traffic without decrypting it.
- **Dual-stack networking:** IPv4 and IPv6 overlay addresses, with forwarding through other mesh members when needed.
- **Peer discovery:** local mDNS discovery, STUN address discovery, and UDP hole punching.
- **Desktop and terminal controls:** a desktop dashboard, CLI, and live terminal dashboard backed by the same system service.
- **No hosted account required:** create a network and share a join string with your other devices.

> **Development status:** Skein is intended for small, trusted meshes of roughly 2–5 devices. Prebuilt releases are not published yet. The installation steps below build from source and install the required dependencies automatically; they do not require you to configure Go, Wails, or Wintun by hand.

[Linux installation](#linux) · [Windows installation](#windows) · [Connect your devices](#connect-your-devices) · [Troubleshooting](#troubleshooting) · [Development](#development)

## Installation

### Get the source

1. While viewing this README on GitHub, choose **Code → Download ZIP** from the same branch. If you already have a checkout, use that instead.
2. Extract the ZIP completely. Do not run the installer from inside the archive.
3. Open a terminal in the extracted folder containing `go.mod`, `gui/`, and `scripts/`, then follow your operating system's instructions below.

An internet connection is needed to download dependencies. Installation needs administrator approval, but **build and launch the desktop as your normal user**, not as root or Administrator.

### Linux

#### Prerequisites

| Distribution family | Supported baseline | Package manager |
| --- | --- | --- |
| Arch Linux and Arch-based distributions | Current rolling release | `pacman` |
| Debian | 13 or newer | `apt-get` |
| Ubuntu | 24.04 or newer | `apt-get` |
| Fedora | 40 or newer | `dnf` |
| RHEL / Rocky Linux / AlmaLinux / CentOS Stream | 10 or newer, with the required desktop packages available | `dnf` |

You also need:

- A normal user account with `sudo` access, Bash, and an internet connection.
- A **systemd-based, writable installation** with a graphical desktop session. Immutable/rpm-ostree systems, non-systemd distributions, and containers are not supported by this desktop installer.
- Kernel TUN support (`/dev/net/tun`). On a VPS or container host, the provider must permit TUN devices.
- A polkit authentication agent in your desktop session. Full desktop environments normally provide one; minimal window managers may need [an authentication agent configured](https://wiki.archlinux.org/title/Polkit#Authentication_agents).

GTK4 and WebKitGTK 6.0 must be available in your distribution's enabled repositories. Older releases without those libraries are not supported by the desktop build. The installer checks availability rather than disabling security features or installing libraries from an unrelated distribution.

#### Install

From the extracted repository folder, run:

```bash
bash ./scripts/install-linux.sh
```

Enter your `sudo` password when prompted. The installer:

1. Detects your distribution and installs **Go**, the C toolchain, GTK/WebKit development headers, and runtime packages through its package manager. Go downloads the toolchain required by `go.mod` when needed.
2. Installs **Wails CLI v3.1.0 automatically** in temporary build tooling, then builds the daemon, CLI, helper, and desktop **without running the build as root**. On Arch, it uses a compatible Node.js/npm already on the build path or downloads Node.js temporarily, without replacing an installed LTS package. Other distributions install Node.js/npm through their package manager and use the temporary download if needed. You do not need to install Go or Wails yourself first.
3. Installs the binaries, desktop menu entry, icon, and administrator-approval policy.
4. Keeps an existing network configuration when upgrading.

Launch **Skein** from your application menu, or run:

```bash
skein-desktop
```

Continue with [Connect your devices](#connect-your-devices). On a fresh installation, the service is registered and started when you create or join a network—not before.

<details>
<summary>Preview the install or use binaries you already built</summary>

Preview the detected distribution, packages, and file locations without changing your system:

```bash
bash ./scripts/install-linux.sh --dry-run
```

To skip compilation, supply a directory containing all four Linux binaries: `skeind`, `skein`, `skein-helper`, and `skein-desktop`. Runtime dependencies are still installed:

```bash
bash ./scripts/install-linux.sh --binary-dir ./gui/bin
```

Run these commands from the source checkout so the installer can also find its desktop entry, icon, and polkit policy.

</details>

### Windows

#### Prerequisites

- **64-bit Windows 10 or Windows 11**, using an administrator account. Run the desktop normally; approve UAC when a privileged operation is requested.
- **Windows PowerShell 5.1 or newer** and an internet connection.
- **Windows Package Manager (`winget`)** for automatic build-tool installation. Check it with `winget --version`. If it is missing, install or update Microsoft's **App Installer** using the [official winget instructions](https://learn.microsoft.com/windows/package-manager/winget/), then reopen PowerShell.

You do **not** need to download Wintun separately. The Setup executable bundles it, and checks for Microsoft Edge WebView2, installing the runtime if needed.

#### Build and install

1. In File Explorer, open the extracted repository folder. Type `powershell` in the address bar and press Enter. You do not need to open an administrator shell for the build.
2. Run:

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-windows-installer.ps1 -InstallPrerequisites -RunInstaller
   ```

3. With **`-InstallPrerequisites`**, the script automatically installs missing **Go, Node.js, Wails CLI v3.1.0, and NSIS 3.x**. Approve the build-tool prompts. It then builds the application and opens the **Skein Setup** wizard; approve its UAC prompt and complete installation.
4. Launch **Skein** from the Start menu and continue with [Connect your devices](#connect-your-devices).

`-ExecutionPolicy Bypass` applies only to that PowerShell process; it does not permanently change your execution policy. Review scripts before running them, and do not disable SmartScreen or antivirus protection to force an installation.

The Setup executable installs the desktop, daemon, helper, CLI, and Wintun together in `%ProgramFiles%\Skein` (normally `C:\Program Files\Skein`). This location is fixed so setup still works after a network reset. On a fresh installation, creating or joining a network registers and starts the service.

<details>
<summary>Build without installing, ARM64, or manual prerequisites</summary>

To build the installer without launching it, omit `-RunInstaller`:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-windows-installer.ps1 -InstallPrerequisites
```

The default x64 output is:

```text
gui\bin\skein-desktop-amd64-installer.exe
```

Run that file on the target Windows machine. The target needs administrator approval and WebView2, but **does not need the build tools**.

For an ARM64 build, add `-Arch arm64`. Do not mix an ARM64 installer or Wintun DLL with an x64 build.

If you cannot use `winget`, install these prerequisites manually, reopen PowerShell, and run the build script without `-InstallPrerequisites`:

| Tool | Requirement |
| --- | --- |
| [Go](https://go.dev/doc/install) | 1.26.10 or newer; the project declares its toolchain requirement in `go.mod` |
| [Node.js](https://nodejs.org/en/download/) | 22.12 or newer, with npm |
| [NSIS](https://nsis.sourceforge.io/Download) | 3.x, providing `makensis.exe` |
| [Wails CLI](https://v3.wails.io/getting-started/installation/) | Exactly v3.1.0 |

Install the pinned Wails CLI after Go:

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-windows-installer.ps1
```

The build script searches the usual installation locations as well as `PATH`.

</details>

## Connect your devices

Install Skein on **each** device first. Leave the default listen port and MTU unchanged unless you have a specific networking requirement.

### 1. Create the network on one device

1. Open Skein and select **Create a network**.
2. Enter a name for this device and select **Create network**.
3. Approve the administrator authentication prompt—polkit on Linux or UAC on Windows.
4. Copy the **network join string** shown after setup.

### 2. Join from another device

1. Open Skein on the other device and select **Join a network**.
2. Paste the network join string and select **Join network**.
3. Approve the authentication prompt. The desktop connects to the newly configured service.

For each additional device, open **Invitations → Join a device** on an already connected device and generate a **fresh network join string**. Join strings contain a snapshot of known addresses; do not reuse an old snapshot for multiple new nodes.

> **Keep network join strings private.** They contain the network secret and allow another device to join. Share them through a trusted channel, not a public issue, screenshot, or log. A **peer invite** is not the same as a **network join string**.

### 3. Check the connection

The desktop shows peers, their overlay addresses, and routes. Use a peer's overlay address to connect to a service running on it. The remote service and that machine's firewall must also permit the connection.

For terminal diagnostics:

**Linux**

```bash
skein status
skein peers
skein routes
skein dashboard
```

**Windows — PowerShell**

```powershell
& "$env:ProgramFiles\Skein\skein.exe" status
& "$env:ProgramFiles\Skein\skein.exe" peers
& "$env:ProgramFiles\Skein\skein.exe" routes
& "$env:ProgramFiles\Skein\skein.exe" dashboard
```

Windows installations do not require adding Skein to `PATH`; the commands above work without doing so. Exit the terminal dashboard with `q`.

### Network requirements

- Skein uses **UDP port 11010 by default**. The daemon applies the relevant local firewall rules when the network connects.
- mDNS helps discover devices on the same LAN. It does not cross routers by itself.
- STUN and hole punching help discover reachable addresses; they cannot guarantee connectivity through every NAT or firewall. A mesh member must be reachable to relay traffic when a direct path is unavailable.
- Do not expose the local control socket or pipe to the internet. Service installations use a Unix socket on Linux and a named pipe on Windows.

## Updating

1. Quit the desktop completely using its tray menu; closing the window may leave it running.
2. Get the updated source and rerun the same installer command for your operating system.
3. Reopen Skein.

The installers stop an existing service before replacing its binaries, preserve the current configuration and identity, and restart a configured service. An update does not require creating or joining the network again. Keep a backup before changing an important installation.

## Uninstall

Quit Skein Desktop from its tray menu, then run:

```bash
skein uninstall
```

On Windows, use PowerShell:

```powershell
& "$env:ProgramFiles\Skein\skein.exe" uninstall
```

Confirm by typing `yes`. This removes the local installation, configuration,
identity, and Skein-managed backups; you will need a new join string to reconnect.
Use `--yes` to confirm noninteractively. Source checkouts and shared dependencies
are not removed. Uninstalling this device does not uninstall its peers.
On Windows, the running CLI may need final cleanup after it exits. The command
reports any pending files and the cleanup log; files still locked are also
scheduled for deletion on restart. A failed cleanup leaves its log in the path
shown. A new installation is not targeted by the pending cleanup.

## Troubleshooting

| Problem | What to check |
| --- | --- |
| Linux reports unsupported distribution or missing WebKitGTK | Check the supported baselines above and your enabled repositories. A `.deb` or `.rpm` cannot make missing GTK/WebKit libraries compatible. |
| Arch reports dependency conflicts involving installed system libraries | Review pacman's errors and bring the whole system up to date with `sudo pacman -Syu`, then retry. If the full upgrade still fails, resolve repository or package conflicts first; do not force or remove core packages. |
| No Linux authentication dialog appears | Run from a local graphical session with a polkit authentication agent. Do not launch the desktop with `sudo`. |
| Windows cannot find `winget` | Install/update Microsoft App Installer, reopen PowerShell, then retry. Alternatively, use the manual prerequisites above. |
| Windows reports an old Node.js version | Install Node.js 22.12+ or a newer LTS release, then rerun the builder. |
| Installer says the desktop is running | Choose **Quit** from Skein's tray menu, then retry. The installer does not forcibly kill the app. |
| Setup reports an existing configuration | Use the existing installation. Do not repeatedly create another network or delete the config files; use the app's explicit reset flow only if you intend to leave the current network. |
| Service is installed but no peers connect | Check the join string, UDP reachability, firewall rules, and whether at least one peer is reachable. STUN alone does not bypass all NATs. |
| The desktop cannot reach the daemon | Check the service status and logs below. Use binaries from the same build; older Windows builds may require an elevated first setup. |

**Linux service diagnostics**

```bash
systemctl status skein --no-pager
sudo journalctl -u skein -n 50 --no-pager
```

**Windows service diagnostics**

```powershell
Get-Service skein
```

The service may not exist yet on a fresh install before the first create/join operation.

System configuration is stored at `/etc/skein/skein.toml` on Linux and `%ProgramData%\Skein\skein.toml` on Windows, with `identity.key` beside it. These are privileged files managed by the daemon, not files the desktop should edit directly.

## Development

The automatic installers above are the simplest way to get a working installation. For development, the core toolchain is **Go 1.26.10+**, **Node.js 22.12+ with npm**, and **Wails v3.1.0**. Linux desktop builds additionally require a C toolchain, pkg-config, GTK4 development headers, and WebKitGTK 6.0 development headers; Windows installer builds require NSIS.

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
```

Ensure Go's binary directory is on `PATH`. From the repository root:

```bash
# Build the CLI and daemon without the desktop toolchain.
go build -o ./dist/skein ./cmd/skein
go build -o ./dist/skeind ./cmd/skeind

# Run backend tests in a disposable privileged environment (see below).
go test ./cmd/... ./internal/...

# Run the desktop development environment after installing GUI dependencies.
cd gui
wails3 dev
```

The bootstrap/IPC integration tests perform real ownership and access checks. Run them in a disposable privileged Linux VM/container—not as root against a live installation, and never by relaxing that installation's file permissions.

Building a binary is not the same as installing the system service; use an installer for a machine-wide installation. Do not ship builds using the `skeindev` tag—it enables development-only overrides.

### Documentation and contributions

- [Wire protocol](docs/protocol.md)
- [Desktop/control API](docs/gui-api.md)
- [Measurements and test procedures](docs/measurements.md)
- [Report a bug or request a feature](https://github.com/Utsav-X-bit/Skein/issues)

For bug reports, include the Skein version, operating system, installation method, reproduction steps, and relevant **redacted** logs. Never attach join strings, network secrets, or `identity.key`.

### License

This repository does not currently include a project-wide `LICENSE` file. Third-party components retain their own licenses; Windows installers include the Wintun license alongside its DLL.
