Unicode true

####
## VaultDock desktop installer (NSIS), MASTER_PLAN section 7.6/section 7.7 (task P1-30).
## Built by `wails build -nsis`, which regenerates wails_tools.nsh from
## wails.json on every build: keep all logic here and in the includes.
##
## Per-machine and administrator only:
##   - program files in %ProgramFiles%\VaultDock (only Program Files
##     subfolders are accepted: a SYSTEM service must never run from a
##     folder a user can write);
##   - VaultDockAgent service registered by vaultdock-agent.exe itself;
##   - %ProgramData%\VaultDock with a locked-down ACL and config\mode.json;
##   - HKLM Run entry for the tray UI;
##   - migration of an old per-user install (C5, migrate-peruser.nsh).
## Components (vss, discovery, reinstall) are NOT bundled or downloaded
## here: the agent downloads and verifies them after install.
##
## vaultdock-agent.exe is built by the wails.json pre-build hook
## (build-agent.go) into payload\<arch>\. Wails only packs the UI binary.
##
## Command line:
##   /S                       silent
##   /MODE=simple|advanced    mode for a fresh install (exit code 2 if invalid)
##   /D=<dir>                 install directory (Program Files subfolder)
##   /RELAUNCH                start the UI (de-elevated) after a silent install
##   /MIGRATE_FROM=<dir> /MIGRATE_SID=<sid>   per-user install to migrate
####

!ifdef REQUEST_EXECUTION_LEVEL
    !if "${REQUEST_EXECUTION_LEVEL}" != "admin"
        !error "VaultDock installs per machine: REQUEST_EXECUTION_LEVEL must be admin (do not build with -installscope user)."
    !endif
!else
    !define REQUEST_EXECUTION_LEVEL "admin"
!endif
!ifdef WAILS_INSTALL_SCOPE
    !if "${WAILS_INSTALL_SCOPE}" != "machine"
        !error "VaultDock installs per machine: WAILS_INSTALL_SCOPE must be machine."
    !endif
!else
    !define WAILS_INSTALL_SCOPE "machine"
!endif

!include "wails_tools.nsh"

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

ManifestDPIAware true

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "nsDialogs.nsh"
!include "WinMessages.nsh"

!include "common.nsh"
!include "modes.nsh"
!include "service.nsh"
!include "migrate-peruser.nsh"

!ifndef VD_PAYLOAD_DIR
    !define VD_PAYLOAD_DIR "payload"
!endif

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Start VaultDock"
!define MUI_FINISHPAGE_RUN_FUNCTION VD_LaunchUI

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "..\..\..\..\LICENSE"
Page custom VD_MigratePageCreate
Page custom VD_ModePageCreate VD_ModePageLeave
Page custom VD_InfoPageCreate
!define MUI_PAGE_CUSTOMFUNCTION_PRE VD_DirPagePre
!define MUI_DIRECTORYPAGE_TEXT_TOP "VaultDock runs a background service, so it must be installed in a folder inside $PROGRAMFILES64."
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!define MUI_COMPONENTSPAGE_TEXT_TOP "Your own files and everything stored on your VaultDock server are never removed. Settings, logs and this PC's device registration are kept unless you tick the box."
!insertmacro MUI_UNPAGE_COMPONENTS
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

## Code signing (O4/D29) stays in CI: .github/workflows/release.yml signs
## VaultDock.exe and the finished setup.exe with scripts/sign.ps1. The
## workflow does not sign the embedded uninstaller yet; when it does, enable
## (NSIS >= 3.08; sign.ps1 exits 0 without a certificate):
#!uninstfinalize 'powershell -NoProfile -ExecutionPolicy Bypass -File "..\..\..\..\scripts\sign.ps1" "%1"' = 0
#!finalize 'signtool sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${INFO_PRODUCTVERSION}-${ARCH}-setup.exe"
InstallDir "$PROGRAMFILES64\${INFO_PRODUCTNAME}"
ShowInstDetails show
ShowUninstDetails show

Var VD_DirOk

# The agent binary next to the UI (same architecture selection as wails.files).
!macro VD_AgentFiles
    !ifdef SUPPORTS_AMD64
        ${If} ${IsNativeAMD64}
            File "/oname=${VD_AGENT_EXE}" "${VD_PAYLOAD_DIR}\amd64\${VD_AGENT_EXE}"
        ${EndIf}
    !endif
    !ifdef SUPPORTS_ARM64
        ${If} ${IsNativeARM64}
            File "/oname=${VD_AGENT_EXE}" "${VD_PAYLOAD_DIR}\arm64\${VD_AGENT_EXE}"
        ${EndIf}
    !endif
!macroend

# VD_DirOk = "1" when $INSTDIR is a subfolder of the 64-bit Program Files
# without "." / ".." components or forward slashes.
Function VD_CheckInstDir
    StrCpy $VD_DirOk "0"
    ${If} $INSTDIR == $PROGRAMFILES64
        Return
    ${EndIf}
    StrCpy $R0 $INSTDIR
    StrCpy $R1 $PROGRAMFILES64
    Call VD_IsUnder
    ${If} $R2 != "1"
        Return
    ${EndIf}
    StrCpy $R8 $INSTDIR
    StrCpy $R9 "\."
    Call VD_StrContains
    ${If} $R7 == "1"
        Return
    ${EndIf}
    StrCpy $R9 "/"
    Call VD_StrContains
    ${If} $R7 == "1"
        Return
    ${EndIf}
    StrCpy $VD_DirOk "1"
FunctionEnd

Function .onVerifyInstDir
    Call VD_CheckInstDir
    ${If} $VD_DirOk != "1"
        Abort
    ${EndIf}
FunctionEnd

# Upgrades keep the existing folder.
Function VD_DirPagePre
    ${If} $VD_IsUpgrade == "1"
        Abort
    ${EndIf}
FunctionEnd

Function .onInit
    !insertmacro wails.checkArchitecture
    SetRegView 64
    SetShellVarContext all

    StrCpy $VD_Relaunch "0"
    ${GetParameters} $R0
    ClearErrors
    ${GetOptions} $R0 "/RELAUNCH" $R1
    ${IfNot} ${Errors}
        StrCpy $VD_Relaunch "1"
    ${EndIf}

    Call VD_GetTokenSid

    # Uses the /D= value before it is replaced below (old per-user updaters
    # pass their own folder).
    Push $INSTDIR
    Call VD_MigrateDetect

    StrCpy $VD_IsUpgrade "0"
    ReadRegStr $0 HKLM "${UNINST_KEY}" "UninstallString"
    ${If} $0 != ""
        StrCpy $VD_IsUpgrade "1"
        ReadRegStr $1 HKLM "${UNINST_KEY}" "InstallLocation"
        ${If} $1 != ""
            StrCpy $INSTDIR $1
        ${EndIf}
    ${EndIf}

    # Silent installs never reach .onVerifyInstDir: enforce the rule here.
    Call VD_CheckInstDir
    ${If} $VD_DirOk != "1"
        StrCpy $INSTDIR "$PROGRAMFILES64\${INFO_PRODUCTNAME}"
    ${EndIf}

    Call VD_DataDirInit
    Call VD_ModeInit
FunctionEnd

Function .onInstSuccess
    ${If} $VD_Relaunch == "1"
    ${AndIf} ${Silent}
        Call VD_LaunchUI
    ${EndIf}
FunctionEnd

Section
    SetShellVarContext all
    SetRegView 64
    Call VD_StopForUpgrade
    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR
    !insertmacro wails.files
    !insertmacro VD_AgentFiles

    Call VD_PrepareDataDir
    Call VD_WriteModeFile
    Call VD_MigrateApply
    Call VD_RegisterService

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    # Upgrades keep the user's choice: a removed desktop shortcut is not recreated.
    ${If} $VD_IsUpgrade != "1"
    ${AndIf} $VD_Relaunch != "1"
        CreateShortcut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    ${EndIf}

    !insertmacro wails.writeUninstaller
    WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    WriteRegDWORD HKLM "${UNINST_KEY}" "NoModify" 1
    WriteRegDWORD HKLM "${UNINST_KEY}" "NoRepair" 1
SectionEnd

# ------------------------------------------------------------- Uninstall --
Function un.onInit
    SetRegView 64
    SetShellVarContext all
FunctionEnd

Section "un.VaultDock program and service" VD_UnSecProgram
    SectionIn RO
    SetShellVarContext all
    SetRegView 64
    !insertmacro VD_UninstallService

    # Recursive delete only inside Program Files (also removes components\).
    StrCpy $1 "$PROGRAMFILES64\"
    StrLen $2 $1
    StrCpy $3 $INSTDIR $2
    ${If} $3 == $1
        RMDir /r "$INSTDIR"
    ${Else}
        Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
        Delete "$INSTDIR\${VD_AGENT_EXE}"
        RMDir "$INSTDIR"
    ${EndIf}

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.deleteUninstaller
SectionEnd

# Unticked by default (and in silent uninstalls): data is kept, so a
# reinstall continues with the same device registration and settings.
# Never touches users' files, backups, or %APPDATA%\VaultDock of an old
# per-user install (its rollback copy).
Section /o "un.Also remove settings, logs and device registration (%ProgramData%\VaultDock)" VD_UnSecData
    SetShellVarContext all
    SetRegView 64
    RMDir /r "$APPDATA\VaultDock"
    DeleteRegKey HKLM "${VD_MACHINE_KEY}"
SectionEnd
