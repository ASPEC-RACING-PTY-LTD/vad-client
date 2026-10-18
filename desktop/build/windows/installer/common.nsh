####
## VaultDock installer: shared variables, paths and string helpers.
## Included by project.nsi before modes.nsh, service.nsh and
## migrate-peruser.nsh. Installer-side only (no un. functions here).
####

!ifndef VD_AGENT_EXE
    !define VD_AGENT_EXE "vaultdock-agent.exe"
!endif
!ifndef VD_SERVICE_NAME
    !define VD_SERVICE_NAME "VaultDockAgent"   ; = service.DefaultName (P0-27)
!endif
!define VD_RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define VD_RUN_VALUE "VaultDock"
# HKLM key only administrators can write. DataDir records that
# %ProgramData%\VaultDock was created and locked down by this installer, so a
# reinstall after "uninstall, keep data" can trust (and keep) it.
!define VD_MACHINE_KEY "Software\VaultDock"

# Well-known SIDs: locale independent (group names are translated).
!define VD_SID_SYSTEM "*S-1-5-18"
!define VD_SID_ADMINS "*S-1-5-32-544"
!define VD_SID_USERS  "*S-1-5-32-545"

!define VD_FILE_ATTRIBUTE_DIRECTORY 0x10
!define VD_FILE_ATTRIBUTE_REPARSE_POINT 0x400

Var VD_DataDir       ; %ProgramData%\VaultDock (known-folder API, not %PROGRAMDATA%)
Var VD_DataTrusted   ; "1" = existing data folder created by this installer
Var VD_IsUpgrade     ; "1" = an admin install already exists (HKLM uninstall key)
Var VD_Relaunch      ; "1" = /RELAUNCH (updater): start the UI after a silent install
Var VD_TokenSid      ; string SID of the installer's (elevated) user, "" if unknown

# ------------------------------------------------------------ helpers --
# $R7 = "1" when $R9 occurs in $R8 (case-insensitive), else "".
# Uses $R4-$R7.
Function VD_StrContains
    StrCpy $R7 ""
    StrLen $R5 $R9
    ${If} $R5 = 0
        Return
    ${EndIf}
    StrCpy $R6 0
    ${Do}
        StrCpy $R4 $R8 $R5 $R6
        ${If} $R4 == ""
            ${Break}
        ${EndIf}
        ${If} $R4 == $R9
            StrCpy $R7 "1"
            ${Break}
        ${EndIf}
        IntOp $R6 $R6 + 1
    ${Loop}
FunctionEnd

# $R2 = "1" when path $R0 is $R1 or inside it (case-insensitive, whole
# path components), else "0". Uses $R2 $R3 $R4.
Function VD_IsUnder
    StrCpy $R2 "0"
    StrLen $R3 $R1
    ${If} $R3 = 0
        Return
    ${EndIf}
    StrCpy $R4 $R0 $R3
    ${If} $R4 == $R1
        StrCpy $R4 $R0 1 $R3
        ${If} $R4 == ""
        ${OrIf} $R4 == "\"
            StrCpy $R2 "1"
        ${EndIf}
    ${EndIf}
FunctionEnd

# $R1 = "1" when $R0 is a plausible user account SID (S-1-5-21-... local or
# domain, S-1-12-1-... Entra ID): only digits and dashes after the prefix.
# Uses $R1-$R9.
Function VD_ValidUserSid
    StrCpy $R1 ""
    StrLen $R3 $R0
    ${If} $R3 < 10
    ${OrIf} $R3 > 184
        Return
    ${EndIf}
    StrCpy $R2 $R0 9
    StrCpy $R4 $R0 9
    ${If} $R2 S!= "S-1-5-21-"
    ${AndIf} $R4 S!= "S-1-12-1-"
        Return
    ${EndIf}
    StrCpy $R6 9
    ${DoWhile} $R6 < $R3
        StrCpy $R9 $R0 1 $R6
        StrCpy $R8 "0123456789-"
        Push $R6
        Call VD_StrContains
        Pop $R6
        ${If} $R7 != "1"
            Return
        ${EndIf}
        IntOp $R6 $R6 + 1
    ${Loop}
    StrCpy $R1 "1"
FunctionEnd

# $R1 = $R0 escaped for a JSON string (backslash and quote). Uses $R1-$R4.
Function VD_JsonEscape
    StrCpy $R1 ""
    StrLen $R3 $R0
    StrCpy $R2 0
    ${DoWhile} $R2 < $R3
        StrCpy $R4 $R0 1 $R2
        ${If} $R4 == "\"
            StrCpy $R4 "\\"
        ${ElseIf} $R4 == '"'
            StrCpy $R4 '\"'
        ${EndIf}
        StrCpy $R1 "$R1$R4"
        IntOp $R2 $R2 + 1
    ${Loop}
FunctionEnd

# Sets VD_TokenSid from the process token (TOKEN_USER). In an over-the-
# shoulder elevation this is the administrator, not the signed-in user.
Function VD_GetTokenSid
    StrCpy $VD_TokenSid ""
    System::Call 'kernel32::GetCurrentProcess() p .r0'
    System::Call 'advapi32::OpenProcessToken(p r0, i 0x0008, *p .r1) i .r2'   ; TOKEN_QUERY
    ${If} $2 = 0
        Return
    ${EndIf}
    System::Call 'advapi32::GetTokenInformation(p r1, i 1, p 0, i 0, *i .r3) i .r2'   ; TokenUser
    ${If} $3 > 0
        System::Alloc $3
        Pop $4
        System::Call 'advapi32::GetTokenInformation(p r1, i 1, p r4, i r3, *i .r3) i .r2'
        ${If} $2 <> 0
            System::Call '*$4(p .r5)'                                        ; TOKEN_USER.User.Sid
            System::Call 'advapi32::ConvertSidToStringSidW(p r5, *p .r6) i .r2'
            ${If} $2 <> 0
                System::Call '*$6(&w256 .r7)'
                StrCpy $VD_TokenSid $7
                System::Call 'kernel32::LocalFree(p r6)'
            ${EndIf}
        ${EndIf}
        System::Free $4
    ${EndIf}
    System::Call 'kernel32::CloseHandle(p r1)'
FunctionEnd

# ---------------------------------------------------------- .onInit part --
# Resolves %ProgramData%\VaultDock and whether an existing folder there can
# be trusted: it must be a real directory (no reparse point) and either an
# admin install exists or the HKLM marker names it.
Function VD_DataDirInit
    # Shell known-folder API, never the environment: an elevated installer
    # inherits user-controlled variables (HKCU\Environment).
    SetShellVarContext all
    StrCpy $VD_DataDir "$APPDATA\VaultDock"

    StrCpy $VD_DataTrusted "0"
    System::Call 'kernel32::GetFileAttributesW(w "$VD_DataDir") i .r0'
    ${If} $0 = -1
        Return
    ${EndIf}
    IntOp $1 $0 & ${VD_FILE_ATTRIBUTE_REPARSE_POINT}
    IntOp $2 $0 & ${VD_FILE_ATTRIBUTE_DIRECTORY}
    ${If} $1 <> 0
    ${OrIf} $2 = 0
        Return
    ${EndIf}
    ReadRegStr $3 HKLM "${VD_MACHINE_KEY}" "DataDir"
    ${If} $VD_IsUpgrade == "1"
    ${OrIf} $3 == $VD_DataDir
        StrCpy $VD_DataTrusted "1"
    ${EndIf}
FunctionEnd

# Starts the UI de-elevated: explorer.exe hands the start to the signed-in
# user's shell, so the UI never runs with the installer's admin token. Not
# used when the installer runs as SYSTEM (managed deployment): HKLM Run
# starts the UI at the next sign-in instead.
Function VD_LaunchUI
    ${If} $VD_TokenSid == "S-1-5-18"
        Return
    ${EndIf}
    Exec '"$WINDIR\explorer.exe" "$INSTDIR\${PRODUCT_EXECUTABLE}"'
FunctionEnd
