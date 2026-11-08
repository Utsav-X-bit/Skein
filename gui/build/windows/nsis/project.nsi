Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows you to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
## 
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the wails_tools.nsh file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "my-project" # Default "skein-desktop"
## !define INFO_COMPANYNAME    "My Company" # Default "Skein"
## !define INFO_PRODUCTNAME    "My Product Name" # Default "Skein"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "0.1.0"
## !define INFO_COPYRIGHT      "(c) Now, My Company" # Default "© 2026, My Company"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
####
## Per-user installs (WAILS_INSTALL_SCOPE=user) are deliberately NOT
## supported: Skein ships a SYSTEM service and a Wintun driver, so the
## complete package is machine-wide, admin-only, under %ProgramFiles%.
####
## Include the wails tools
####
!include "wails_tools.nsh"

# Fail the build, not the user, if someone asks for a scope this
# installer cannot deliver. A per-user copy would install a VPN
# service and driver into $LOCALAPPDATA without the admin-only ACLs
# the daemon/helper trust.
!if "${WAILS_INSTALL_SCOPE}" == "user"
    !error "Skein: per-user installs are not supported. The complete VPN package is machine-wide under %ProgramFiles% and requires administrator rights."
!endif
!if "${REQUEST_EXECUTION_LEVEL}" != "admin"
    !error "Skein: the installer must request administrator rights (REQUEST_EXECUTION_LEVEL=admin)."
!endif

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

# Finish action: launch the desktop, but never as an elevated child
# of this installer. The installer runs as admin; launching the GUI
# with Exec here would inherit that token, and the desktop is meant
# to run with the logged-on user's normal token (helper elevation is
# per-operation). MUI_FINISHPAGE_RUN must be defined for MUI2 to show
# the checkbox; the function takes precedence over that value.
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_FUNCTION "SkeinLaunchDesktop"
!define MUI_FINISHPAGE_RUN_TEXT "Launch Skein Desktop (create or join a network)"
!define MUI_FINISHPAGE_TEXT "Skein is installed. Launch it below to create or join a network. Your configuration and identity are kept across upgrades and uninstalls."

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
# Step 11: single directory, NOT $PROGRAMFILES64\Skein\Skein.
# The old company\product layout did not match the GUI's
# canonical fallback (C:\Program Files\Skein\skein-helper.exe,
# gui/services/helper_command_windows.go), so the fallback
# pointed at a directory the installer never created (Agent D
# P1). All exes live in this one directory; the GUI finds the
# helper beside its own exe first and this directory second.
# Bootstrap and reset both discover the daemon in this fixed package
# directory. Do not offer a custom destination that cannot be reconfigured.
InstallDir "$PROGRAMFILES64\${INFO_PRODUCTNAME}"
ShowInstDetails show # This will always show the installation details.

# Running-desktop precheck. A running skein-desktop.exe locks its own
# exe, so both install and uninstall fail halfway through: NSIS cannot
# replace a locked file (the copy aborts after the service was already
# stopped), and RMDir /r cannot delete it (the uninstaller would report
# success with the exe left behind and the uninstall key gone). The same
# lock is what the user hit by hand: "copy .\gui\bin\skein-desktop.exe
# .\dist\: The process cannot access the file ... being used by another
# process".
#
# The probe only ever *reads* process state (tasklist through nsExec);
# it never kills the desktop. It fails closed: if tasklist cannot be
# run, or prints something this script cannot classify, the state stays
# "unknown" and the installer refuses to touch anything rather than
# assuming the desktop is closed.
#
# The desktop's window Close button only hides it to the system tray;
# the process (and its locked exe) stays alive. The refusal text must
# therefore tell the user to Quit from the tray menu, never "close the
# window".
#
# A match is printed as CSV starting with the quoted image name
# ("skein-desktop.exe","1234",...); the no-match notice is localized
# prose and never starts with a quote, so the first character decides.
# The UNIQ parameter keeps the two insertions (installer and
# uninstaller) on distinct labels.
Var SkeinDesktopState

!macro SkeinDesktopProbe UNIQ
    StrCpy $SkeinDesktopState "unknown"
    nsExec::ExecToStack '"$SYSDIR\tasklist.exe" /FI "IMAGENAME eq ${PRODUCT_EXECUTABLE}" /NH /FO CSV'
    Pop $0
    Pop $1
    # $0 is tasklist's exit code: anything but 0 leaves "unknown".
    StrCmp $0 "0" 0 skeinProbe${UNIQ}Done
    # No output at all is not a documented tasklist result: "unknown".
    StrCmp $1 "" skeinProbe${UNIQ}Done
    # A match is CSV and starts with the quoted image name; the
    # no-match notice is prose and never starts with a quote.
    StrCpy $2 $1 1
    StrCmp $2 '"' skeinProbe${UNIQ}Running 0
    StrCpy $SkeinDesktopState "closed"
    Goto skeinProbe${UNIQ}Done
    skeinProbe${UNIQ}Running:
    StrCpy $SkeinDesktopState "running"
    skeinProbe${UNIQ}Done:
!macroend

# The installer is machine-wide and uses the same canonical package
# directory for initial setup, upgrades, and setup after a network reset.
# Sets $R9 to "ok" or "bad"; clobbers $0, $1 and $R1.
Function SkeinValidateTrustedDir
    StrCpy $R9 "bad"
    # NSIS GetFullPathName empties the result for a fresh, absent directory.
    # The Windows API normalizes ".." without requiring that directory.
    System::Call 'kernel32::GetFullPathNameW(w "$INSTDIR", i ${NSIS_MAX_STRLEN}, w .r0, p 0) i .r1'
    IntCmp $1 0 skeinDirDone skeinDirDone
    IntCmp $1 ${NSIS_MAX_STRLEN} skeinDirDone 0 skeinDirDone
    StrCpy $INSTDIR $0
    # Normalize an optional trailing separator.
    StrCpy $R1 "$INSTDIR" "" -1
    StrCmp $R1 "\" 0 skeinDirCanonical
        StrCpy $INSTDIR "$INSTDIR" -1
    skeinDirCanonical:
    StrCmp $INSTDIR "$PROGRAMFILES64\${INFO_PRODUCTNAME}" 0 skeinDirDone
    StrCpy $R9 "ok"
    skeinDirDone:
FunctionEnd


# Verify WebView2 from the registry AFTER the bootstrapper ran. The
# wails macro ignores the bootstrapper's exit code and its "ok" label
# only means "was already installed or launch attempted", so a launch
# alone must never be reported as success. This check is the one that
# decides; a missing runtime aborts before any Skein file is copied.
!macro SkeinVerifyWebView2
    !ifndef WAILS_SKIP_WEBVIEW2
    SetRegView 64
    ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    StrCmp $0 "" 0 skeinWebView2Ok
    ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    StrCmp $0 "" skeinWebView2Missing skeinWebView2Ok
    skeinWebView2Missing:
        DetailPrint "WebView2 Runtime is not installed after the bootstrapper ran."
        IfSilent skeinWebView2Silent
        MessageBox MB_OK|MB_ICONSTOP "Skein could not install the Microsoft Edge WebView2 Runtime, which the desktop app requires. Install the Evergreen WebView2 Runtime, then run this installer again. Nothing has been installed."
        skeinWebView2Silent:
        SetErrorLevel 1
        Abort
    skeinWebView2Ok:
    !endif
!macroend

# Finish-page action. The installer runs elevated; handing the path to
# the interactive shell starts the desktop with the logged-on user's
# normal token instead of inheriting this installer's admin token.
Function SkeinLaunchDesktop
    Exec '"$WINDIR\explorer.exe" "$INSTDIR\${PRODUCT_EXECUTABLE}"'
FunctionEnd

Function .onInit
   !insertmacro wails.checkArchitecture

   # Before any page, file copy, or service mutation.
   !insertmacro SkeinDesktopProbe Install
   StrCmp $SkeinDesktopState "closed" skeinDesktopInstallOk
   StrCmp $SkeinDesktopState "running" 0 skeinDesktopInstallUnknown
   StrCpy $R0 "Skein Desktop is still running. Quit it from its system-tray menu, then start this installer again. Nothing has been changed yet."
   Goto skeinDesktopInstallRefuse
   skeinDesktopInstallUnknown:
   StrCpy $R0 "Could not check whether Skein Desktop is running (tasklist exit $0). Quit Skein Desktop from its system-tray menu and run this installer again; if this keeps happening, reboot and retry. Nothing has been changed yet."
   skeinDesktopInstallRefuse:
   DetailPrint "$R0"
   IfSilent skeinDesktopInstallSilent
   MessageBox MB_OK|MB_ICONSTOP "$R0"
   Quit
   skeinDesktopInstallSilent:
   SetErrorLevel 1
   Abort
   skeinDesktopInstallOk:

FunctionEnd

Section
    !insertmacro wails.setShellContext

    # The same fixed directory must work before and after a network reset.
    # Validate here too so silent /D overrides cannot bypass the constraint.
    Call SkeinValidateTrustedDir
    StrCmp $R9 "ok" installDirOk
    DetailPrint "Skein must be installed in $PROGRAMFILES64\${INFO_PRODUCTNAME}."
    IfSilent installDirFailedSilent
    MessageBox MB_OK|MB_ICONSTOP "Skein must be installed in $PROGRAMFILES64\${INFO_PRODUCTNAME}, so setup and network resets can find the daemon. Run this installer without a custom /D destination. Nothing has been installed."
    installDirFailedSilent:
    SetErrorLevel 1
    Abort
    installDirOk:

    # 1. WebView2: install it if missing, then verify it from the
    #    registry AFTER the bootstrapper ran. The wails macro's "ok"
    #    label only means "already installed or launch attempted"; a
    #    launch is not an install, so a missing runtime aborts here,
    #    before any Skein file is copied.
    !insertmacro wails.webview2runtime
    !insertmacro SkeinVerifyWebView2

    SetOutPath $INSTDIR

    # Complete-setup install (M9 §2f): the installer is the whole
    # install, not just a file copy.
    #   0. .onInit already refused to start while skein-desktop.exe
    #      is running (the desktop locks its own exe, so the copy
    #      below would abort after the service had been stopped).
    #   1. Stop a running service FIRST (upgrade case): the helper
    #      is already in $INSTDIR from a previous install, and a
    #      running skeind locks its exe, so the File lines below
    #      would fail on locked files. The helper stop tolerates
    #      already-stopped (1062) and no-service (1060). Any other
    #      failure aborts BEFORE a file is overwritten: carrying on
    #      would replace some binaries around a service that is
    #      still running. A fresh install has no helper yet, so this
    #      is skipped.
    #   2. Copy the files (desktop via wails.files, system files
    #      via the File lines below) and verify each one exists.
    #   3. If a network already exists, re-register + start the
    #      service through the helper (install verb, pinned to this
    #      exact $INSTDIR\skeind.exe). On a fresh install there is
    #      no config yet, and helper install fails by design ("no
    #      system config: run init or import first") — the service
    #      is registered by the bootstrap flow when the user creates
    #      or joins a network, so the installer skips it and says so
    #      instead of failing.
    # scripts/packaging-assert.sh enforces this order (stop before
    # the first File line; helper install gated on an existing
    # network config; Abort on helper-install failure).
    IfFileExists "$INSTDIR\skein-helper.exe" installStopService installCopyFiles
    installStopService:
        DetailPrint "Stopping running skein service for upgrade..."
        ExecWait '"$INSTDIR\skein-helper.exe" stop' $0
        StrCmp $0 0 installCopyFiles
        DetailPrint "Service stop failed (helper stop exit $0); aborting before overwriting installed files."
        IfSilent installStopFailedSilent
        MessageBox MB_OK|MB_ICONSTOP "Skein install failed: the running service could not be stopped (helper stop exit $0), so no files were replaced. Stop it manually ($SYSDIR\sc.exe stop skein) and run the installer again."
        installStopFailedSilent:
        SetErrorLevel 1
        Abort
    installCopyFiles:

    !insertmacro wails.files

    # Step 11 (ADR-001 collapse): the installer ships the whole
    # system side in this same directory, not just the desktop exe
    # (the old failure mode: wails.files alone ships only the
    # desktop exe). Every path below is a makensis -D define passed
    # by build/windows/Taskfile.yml create:nsis:installer, which
    # builds the system binaries and fetches Wintun (pinned 0.14.1,
    # SHA-256-verified by scripts/fetch-wintun.ps1/.sh — the build
    # fails on a hash mismatch, it never ships an unverified DLL)
    # before invoking makensis. A missing define is a hard !error,
    # not a silently skipped file: an installer that cannot ship
    # the daemon must not build. scripts/packaging-assert.sh parses
    # this section and fails if any required File line is removed.
    !ifndef ARG_SKEIND_BINARY
        !error "Skein: ARG_SKEIND_BINARY is required (build skeind for Windows first)"
    !endif
    !ifndef ARG_SKEIN_HELPER_BINARY
        !error "Skein: ARG_SKEIN_HELPER_BINARY is required (build skein-helper for Windows first)"
    !endif
    !ifndef ARG_SKEIN_CLI_BINARY
        !error "Skein: ARG_SKEIN_CLI_BINARY is required (build skein for Windows first)"
    !endif
    !ifndef ARG_WINTUN_DLL
        !error "Skein: ARG_WINTUN_DLL is required (fetch Wintun for the target arch first)"
    !endif
    !ifndef ARG_WINTUN_LICENSE
        !error "Skein: ARG_WINTUN_LICENSE is required (fetch Wintun for the target arch first)"
    !endif
    # Missing build inputs must fail compilation; runtime write failures
    # are checked before service registration below.
    ClearErrors
    File "/oname=skeind.exe" "${ARG_SKEIND_BINARY}"
    IfErrors installCopyFailed
    ClearErrors
    File "/oname=skein-helper.exe" "${ARG_SKEIN_HELPER_BINARY}"
    IfErrors installCopyFailed
    ClearErrors
    File "/oname=skein.exe" "${ARG_SKEIN_CLI_BINARY}"
    IfErrors installCopyFailed
    ClearErrors
    File "/oname=wintun.dll" "${ARG_WINTUN_DLL}"
    IfErrors installCopyFailed
    ClearErrors
    File "/oname=wintun-LICENSE.txt" "${ARG_WINTUN_LICENSE}"
    IfErrors installCopyFailed

    # Detect a copy failure even if NSIS swallowed the write error:
    # a missing required file is a failed install, never a success.
    IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 installCopyFailed
    IfFileExists "$INSTDIR\skeind.exe" 0 installCopyFailed
    IfFileExists "$INSTDIR\skein-helper.exe" 0 installCopyFailed
    IfFileExists "$INSTDIR\skein.exe" 0 installCopyFailed
    IfFileExists "$INSTDIR\wintun.dll" 0 installCopyFailed
    IfFileExists "$INSTDIR\wintun-LICENSE.txt" 0 installCopyFailed
    Goto installCopyOk
    installCopyFailed:
        DetailPrint "Copying the Skein files into $INSTDIR failed."
        IfSilent installCopyFailedSilent
        MessageBox MB_OK|MB_ICONSTOP "Skein install failed: a file could not be written to $INSTDIR. Close anything using these files, make sure you are running the installer as administrator, and try again. The installed files may be incomplete."
        installCopyFailedSilent:
        SetErrorLevel 1
        Abort
    installCopyOk:

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    # 3. Service: re-register + start when a network already exists.
    # $PROGRAMDATA is NOT an NSIS constant (see the uninstall
    # section): resolve it from the environment instead.
    ReadEnvStr $R9 "ProgramData"
    IfFileExists "$R9\Skein\skein.toml" installRegisterService installFreshInstall
    installRegisterService:
        DetailPrint "Registering and starting the skein service..."
        # Pin registration to this complete installed payload.
        ExecWait '"$INSTDIR\skein-helper.exe" install --daemon "$INSTDIR\skeind.exe"' $0
        StrCmp $0 0 installServiceOk
        DetailPrint "Skein service registration failed (helper install exit $0)."
        IfSilent installServiceFailedSilent
        MessageBox MB_OK|MB_ICONSTOP "Skein install failed: the helper could not register and start the service (helper install exit $0). The files are installed in $INSTDIR. Run this from an elevated prompt to finish the install: $\"$INSTDIR\skein-helper.exe$\" install --daemon $\"$INSTDIR\skeind.exe$\""
    installServiceFailedSilent:
        SetErrorLevel 1
        Abort
    installServiceOk:
        DetailPrint "Skein service is running."
        Goto installServiceDone
    installFreshInstall:
        DetailPrint "No network configured yet; the service will be registered when you create or join a network."
    installServiceDone:

    # Record the exact directory for the next upgrade (.onInit reads it
    # back and revalidates it). wails.writeUninstaller owns the rest of
    # the key and runs after this.
    SetRegView 64
    WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    # Refuse to uninstall over a running desktop, before the service is
    # touched: RMDir /r cannot delete a locked skein-desktop.exe, so the
    # uninstall would delete the shortcuts and the uninstall key and
    # still leave the exe behind. Same probe as the installer; nothing
    # below runs when the desktop is running or the probe is unsure.
    !insertmacro SkeinDesktopProbe Uninstall
    StrCmp $SkeinDesktopState "closed" unSkeinDesktopOk
    StrCmp $SkeinDesktopState "running" 0 unSkeinDesktopUnknown
    StrCpy $R0 "Skein Desktop is still running. Quit it from its system-tray menu, then run the uninstaller again. Nothing has been removed yet."
    Goto unSkeinDesktopRefuse
    unSkeinDesktopUnknown:
    StrCpy $R0 "Could not check whether Skein Desktop is running (tasklist exit $0). Quit Skein Desktop from its system-tray menu and run the uninstaller again; if this keeps happening, reboot and retry. Nothing has been removed yet."
    unSkeinDesktopRefuse:
    DetailPrint "$R0"
    IfSilent unSkeinDesktopSilent
    MessageBox MB_OK|MB_ICONSTOP "$R0"
    Quit
    unSkeinDesktopSilent:
    SetErrorLevel 1
    Abort
    unSkeinDesktopOk:

    !insertmacro wails.setShellContext

    # Step 11 uninstall order (ADR-001 plan §7.1). The old section
    # deleted $INSTDIR first and never touched the service at all,
    # leaving a running SYSTEM service behind (Agent D P1) — and
    # deleting the helper before it could ever be run.
    #   0. The desktop precheck above already refused to run while
    #      skein-desktop.exe is running (RMDir /r cannot delete a
    #      locked exe, so the uninstall would leave it behind and
    #      still report success).
    #   1. Run the minimal helper's uninstall verb FIRST: it stops
    #      the service before deleting it (Step 10 stop-then-delete
    #      fix, tolerating 1062 already-stopped and 1060 no-service
    #      for idempotence) and removes Skein's firewall rules.
    #      If it fails, the uninstall ABORTS with a message rather
    #      than deleting files around a running service. The one
    #      tolerated case is a helper binary already deleted by
    #      hand: then there is nothing to run, and uninstall
    #      proceeds with a warning (the service, if still present,
    #      is reported, not silently left — see below).
    #   2. Purge question: uninstall WITHOUT purge leaves
    #      %ProgramData%\Skein (config, identity, access, backups)
    #      in place and says so. WITH purge, the whole directory is
    #      MOVED to %ProgramData%\Skein.bak — a backup, not a
    #      silent delete — so a purge is recoverable. If a .bak
    #      already exists, purge leaves the config in place and
    #      tells the user to clear the old backup first, rather
    #      than overwriting it. --purge on the helper verb itself
    #      is still accepted-but-ignored (Step 10): config purge
    #      is this section plus the daemon reset endpoint, never
    #      the helper alone.
    #   3. Only then: WebView2 datapath, $INSTDIR, shortcuts,
    #      associations, uninstaller registry keys.
    # .old / backup retention: this section never deletes *.old
    # files or backup directories in $INSTDIR or %ProgramData%\Skein
    # on upgrade or uninstall-without-purge, and a purge MOVES them
    # (inside %ProgramData%\Skein) to .bak instead of deleting them.
    # Wintun adapters are NOT forcibly deleted here: uninstall
    # cannot reliably identify Skein's adapter from other
    # software's (Agent D P2), so it reports that any Skein adapter
    # may remain for manual cleanup rather than deleting an
    # adapter it cannot prove is Skein's.
    IfFileExists "$INSTDIR\skein-helper.exe" helperPresent helperMissing
    helperPresent:
        ExecWait '"$INSTDIR\skein-helper.exe" uninstall' $0
        ${If} $0 != 0
            DetailPrint "Skein uninstall failed: helper uninstall exit $0; nothing was deleted."
            IfSilent unHelperFailedSilent
            MessageBox MB_OK|MB_ICONSTOP "Skein uninstall failed: the helper could not stop and remove the service (exit $0). Nothing was deleted, so no orphaned service is left behind. Fix the service state and run the uninstaller again."
            unHelperFailedSilent:
            SetErrorLevel 1
            Abort
        ${EndIf}
        Goto helperDone
    helperMissing:
        DetailPrint "Warning: skein-helper.exe not found in $INSTDIR; service uninstall skipped. If 'sc query skein' still shows the service, remove it by hand (sc stop skein, then sc delete skein)."
    helperDone:

    # $PROGRAMDATA is NOT an NSIS constant: makensis warning 6000
    # compiles it to empty, which silently disabled this whole purge
    # branch (M9 §2c: the "Yes" answer always reported "No Skein
    # configuration found" and no move ever happened). Resolve the
    # real all-users application-data directory from the environment
    # instead; ProgramData is a system variable, identical for every
    # user, so this is correct for machine installs.
    ReadEnvStr $R9 "ProgramData"
    # Silent uninstall must never purge: IfSilent jumps straight
    # to noPurge, so the question is only asked interactively.
    # (M9 §2d: the bare question ended a silent uninstall at the
    # MessageBox — instrumented section executed nothing after it
    # — and /SD IDNO alone did not hold the no-purge branch in the
    # shipped uninstaller, so the bypass is explicit. /SD IDNO is
    # kept as a second guard for any other silent path.)
    IfSilent noPurge
    MessageBox MB_YESNO "Remove Skein configuration and identity in %ProgramData%\Skein? Choose No to keep it (you can reinstall later without rejoining). Choosing Yes moves it to %ProgramData%\Skein.bak instead of deleting it." /SD IDNO IDYES doPurge IDNO noPurge
    doPurge:
        IfFileExists "$R9\Skein" purgeCheckBak purgeNone
    purgeNone:
        DetailPrint "No Skein configuration found at %ProgramData%\Skein; nothing to purge"
        Goto purgeDone
    purgeCheckBak:
        IfFileExists "$R9\Skein.bak" purgeBlocked purgeMove
    purgeBlocked:
        MessageBox MB_OK "A backup already exists at %ProgramData%\Skein.bak, so the configuration was left in place rather than overwriting that backup. Remove or rename the old backup first if you want to purge."
        Goto noPurge
    purgeMove:
        Rename "$R9\Skein" "$R9\Skein.bak"
        DetailPrint "Skein configuration moved to %ProgramData%\Skein.bak"
        Goto purgeDone
    noPurge:
        DetailPrint "Skein configuration kept at %ProgramData%\Skein"
    purgeDone:

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath (this app's data, not the shared runtime)

    # Only this app's own directory: the shared WebView2 Runtime
    # installation and the Wintun driver/adapter are never removed here.
    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
