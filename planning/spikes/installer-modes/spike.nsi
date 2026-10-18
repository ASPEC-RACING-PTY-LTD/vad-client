Unicode true

####
## Standalone harness for the P0-S5 spike: exercises vaultdock_modes.nsh
## without Wails. Compile only (from this folder):
##   makensis /DOUTDIR=<scratch> spike.nsi
## Pass /DVD_PAYLOAD_DIR=<dir with VaultDock.exe and vaultdock-agent.exe>
## to include real binaries; without it, no program files are copied and the
## service registration step is skipped (a VM test would otherwise register a
## service pointing at a missing binary).
## Never run the output on a development machine: it needs admin rights,
## changes ACLs under %ProgramData% and registers a service. Use a VM.
####

!ifndef OUTDIR
    !define OUTDIR "."
!endif

# Stand-ins for what wails_tools.nsh provides in the real build.
!define INFO_PROJECTNAME    "VaultDock"
!define INFO_COMPANYNAME    "VaultDock"
!define INFO_PRODUCTNAME    "VaultDock"
!define INFO_PRODUCTVERSION "0.1.0"
!define PRODUCT_EXECUTABLE  "VaultDock.exe"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\VaultDockVaultDock-spike"

RequestExecutionLevel admin
Name "VaultDock (installer-modes spike)"
OutFile "${OUTDIR}\vaultdock-modes-spike.exe"
InstallDir "$PROGRAMFILES64\VaultDock"
ShowInstDetails show
ManifestDPIAware true

!include "MUI2.nsh"
!include "x64.nsh"
!include "vaultdock_modes.nsh"

!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
Page custom VD_ModePageCreate VD_ModePageLeave
Page custom VD_InfoPageCreate
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Function .onInit
    ${IfNot} ${RunningX64}
        MessageBox MB_ICONSTOP "64-bit Windows is required." /SD IDOK
        Quit
    ${EndIf}
    Call VD_Init
FunctionEnd

Function .onVerifyInstDir
    Call VD_VerifyInstDir
FunctionEnd

Section "VaultDock"
    SetShellVarContext all
    SetRegView 64
    Call VD_StopForUpgrade

    SetOutPath "$INSTDIR"
    !ifdef VD_PAYLOAD_DIR
        File "/oname=${PRODUCT_EXECUTABLE}" "${VD_PAYLOAD_DIR}\${PRODUCT_EXECUTABLE}"
        File "/oname=${VD_AGENT_EXE}" "${VD_PAYLOAD_DIR}\${VD_AGENT_EXE}"
    !endif
    # Components are NOT bundled: the agent downloads vss/discovery/reinstall
    # after install according to the mode file (see README).

    Call VD_PrepareDataDir
    Call VD_WriteModeFile
    !ifdef VD_PAYLOAD_DIR
        Call VD_RegisterService
    !else
        DetailPrint "Spike build without payload: service registration skipped."
    !endif

    WriteUninstaller "$INSTDIR\uninstall.exe"
    WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "VaultDock (spike)"
    WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
    WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
SectionEnd

Section "uninstall"
    SetShellVarContext all
    SetRegView 64
    !insertmacro VD_UninstallService
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Delete "$INSTDIR\${VD_AGENT_EXE}"
    Delete "$INSTDIR\uninstall.exe"
    RMDir "$INSTDIR"
    MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "Also remove VaultDock settings and state from $APPDATA\VaultDock?$\r$\nBackups on your server are not affected." /SD IDNO IDNO keep
        RMDir /r "$APPDATA\VaultDock"
    keep:
    DeleteRegKey HKLM "${UNINST_KEY}"
SectionEnd
