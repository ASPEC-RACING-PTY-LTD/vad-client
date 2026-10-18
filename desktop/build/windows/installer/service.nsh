####
## VaultDock installer: %ProgramData%\VaultDock ACLs, the VaultDockAgent
## service and the HKLM Run entry (MASTER_PLAN section 7.6). Productised from spike
## P0-S5. Needs common.nsh and modes.nsh (VD_EffectiveMode).
##
## The service is registered by the agent binary itself
## (`vaultdock-agent install -mode <m>`, P0-27), so the SCM settings live in
## one place (desktop/internal/service): LocalSystem, delayed auto start,
## quoted binary path + "service", SID type unrestricted, RequiredPrivileges
## per mode, restart on failure (5 s / 30 s / 2 min, reset 24 h, non-crash
## flag), 60 s preshutdown, event log source. sc.exe is only a fallback.
####

# RequiredPrivileges, kept equal to service.SimplePrivileges and
# service.AdvancedExtraPrivileges (P0-27). Used by the sc.exe fallback only.
!define VD_PRIVS_SIMPLE "SeChangeNotifyPrivilege/SeImpersonatePrivilege/SeTcbPrivilege/SeCreateGlobalPrivilege"
!define VD_PRIVS_ADVANCED "${VD_PRIVS_SIMPLE}/SeBackupPrivilege/SeManageVolumePrivilege"

# Protected DACL for %ProgramData%\VaultDock, applied atomically at creation:
# owner Administrators, no inherited entries, SYSTEM and Administrators full.
!define VD_DATA_SDDL "O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

!macro VD_Icacls ARGS
    nsExec::ExecToLog '"$SYSDIR\icacls.exe" ${ARGS}'
    Pop $0
    ${If} $0 != 0
        DetailPrint 'icacls ${ARGS} failed: $0'
        MessageBox MB_ICONSTOP "VaultDock could not secure its data folder (error $0). Setup will stop to keep your PC safe." /SD IDOK
        Abort
    ${EndIf}
!macroend

!macro VD_Sc ARGS
    nsExec::ExecToLog '"$SYSDIR\sc.exe" ${ARGS}'
    Pop $0
!macroend

# --------------------------------------------------------------- Install --
# Stops the agent and every UI instance (all sessions) before files are
# replaced. The installed agent binary stops the service and waits for
# SERVICE_STOPPED; net.exe is the fallback (its exit codes are not
# localised). TODO(P1-29/P0-28): ask UIs to exit over IPC before taskkill.
Function VD_StopForUpgrade
    ${If} ${FileExists} "$INSTDIR\${VD_AGENT_EXE}"
        nsExec::ExecToLog '"$INSTDIR\${VD_AGENT_EXE}" stop -timeout 60s'
        Pop $0
    ${Else}
        # First install: no service yet, so skip net stop ("The service name
        # is invalid" would only confuse).
        nsExec::Exec '"$SYSDIR\sc.exe" query ${VD_SERVICE_NAME}'
        Pop $0
        ${If} $0 == 0
            nsExec::ExecToLog '"$SYSDIR\net.exe" stop ${VD_SERVICE_NAME}'
            Pop $0
        ${EndIf}
    ${EndIf}
    DetailPrint "Stop ${VD_SERVICE_NAME}: exit $0"
    nsExec::Exec '"$SYSDIR\taskkill.exe" /IM "${VD_AGENT_EXE}" /F'
    Pop $0
    nsExec::Exec '"$SYSDIR\taskkill.exe" /IM "${PRODUCT_EXECUTABLE}" /F'
    Pop $0
    Sleep 800
FunctionEnd

# Creates or re-secures %ProgramData%\VaultDock:
#   root    owner Administrators, protected DACL, SYSTEM + Administrators full
#   config  + Users read (the UI reads mode.json; nothing secret lives here)
#   state, keys, logs, spool: inherited only (no Users access)
# An existing folder that this installer did not create (any user may create
# folders in ProgramData) or a reparse point is moved aside and never
# trusted: no recursive ACL change ever runs over a tree a user could have
# seeded with hard links or junctions.
Function VD_PrepareDataDir
    ${If} $VD_DataTrusted != "1"
        System::Call 'kernel32::GetFileAttributesW(w "$VD_DataDir") i .r0'
        ${If} $0 <> -1
            System::Call 'kernel32::GetTickCount() i .r2'
            DetailPrint "Untrusted $VD_DataDir found; moving it aside."
            ClearErrors
            Rename "$VD_DataDir" "$VD_DataDir.untrusted-$2"
            ${If} ${Errors}
                MessageBox MB_ICONSTOP "An existing folder $VD_DataDir could not be checked or moved. Remove it and run Setup again." /SD IDOK
                Abort
            ${EndIf}
        ${EndIf}

        # Create the folder with its final DACL in one call, so there is no
        # moment in which it inherits ProgramData's "Users may create" entry.
        System::Call 'advapi32::ConvertStringSecurityDescriptorToSecurityDescriptorW(w "${VD_DATA_SDDL}", i 1, *p .r1, p 0) i .r0'
        ${If} $0 = 0
            MessageBox MB_ICONSTOP "VaultDock could not prepare a secure data folder. Setup will stop to keep your PC safe." /SD IDOK
            Abort
        ${EndIf}
        !if ${NSIS_PTR_SIZE} > 4
            System::Call '*(i 24, p r1, i 0) p .r2'    ; SECURITY_ATTRIBUTES (64-bit)
        !else
            System::Call '*(i 12, p r1, i 0) p .r2'    ; SECURITY_ATTRIBUTES (32-bit)
        !endif
        System::Call 'kernel32::CreateDirectoryW(w "$VD_DataDir", p r2) i .r0'
        System::Free $2
        System::Call 'kernel32::LocalFree(p r1)'
        ${If} $0 = 0
            # Includes ERROR_ALREADY_EXISTS: someone re-created it after the move.
            MessageBox MB_ICONSTOP "VaultDock could not create $VD_DataDir. Setup will stop to keep your PC safe." /SD IDOK
            Abort
        ${EndIf}
    ${EndIf}

    # Re-assert the root ACL (repairs drift on a trusted folder; a no-op on
    # a new one). Not recursive.
    !insertmacro VD_Icacls '"$VD_DataDir" /setowner ${VD_SID_ADMINS}'
    !insertmacro VD_Icacls '"$VD_DataDir" /inheritance:r /grant:r ${VD_SID_SYSTEM}:(OI)(CI)F ${VD_SID_ADMINS}:(OI)(CI)F'

    CreateDirectory "$VD_DataDir\config"
    CreateDirectory "$VD_DataDir\state"
    CreateDirectory "$VD_DataDir\keys"
    CreateDirectory "$VD_DataDir\logs"
    CreateDirectory "$VD_DataDir\spool"
    !insertmacro VD_Icacls '"$VD_DataDir\config" /grant ${VD_SID_USERS}:(OI)(CI)RX'

    SetRegView 64
    WriteRegStr HKLM "${VD_MACHINE_KEY}" "DataDir" "$VD_DataDir"
FunctionEnd

# Registers (or reconfigures on upgrade) the agent service for the
# effective mode, adds the HKLM Run entry for the tray UI and starts the
# service. A failed start is not fatal: the files are installed and the UI
# reports the service state.
Function VD_RegisterService
    Call VD_EffectiveMode
    StrCpy $7 $R0
    DetailPrint "Registering the ${VD_SERVICE_NAME} service ($7 mode)."
    nsExec::ExecToLog '"$INSTDIR\${VD_AGENT_EXE}" install -mode $7'
    Pop $0
    ${If} $0 != 0
        DetailPrint "vaultdock-agent install failed ($0); using sc.exe instead."
        Call VD_RegisterServiceSc
    ${EndIf}

    # Tray UI for every user who signs in. The UI starts hidden.
    SetRegView 64
    WriteRegStr HKLM "${VD_RUN_KEY}" "${VD_RUN_VALUE}" '"$INSTDIR\${PRODUCT_EXECUTABLE}" --autostart --minimized'

    nsExec::ExecToLog '"$INSTDIR\${VD_AGENT_EXE}" start'
    Pop $0
    ${If} $0 != 0
        DetailPrint "The VaultDock service did not start (exit $0). VaultDock shows the service state when it opens."
    ${EndIf}
FunctionEnd

# Fallback: the same registration with sc.exe ($7 = mode). Create/config
# and privilege failures abort; cosmetic settings only log.
Function VD_RegisterServiceSc
    StrCpy $6 'binPath= "\"$INSTDIR\${VD_AGENT_EXE}\" service" start= delayed-auto obj= LocalSystem DisplayName= "VaultDock Agent"'
    !insertmacro VD_Sc 'create ${VD_SERVICE_NAME} $6'
    ${If} $0 == 1073                      ; ERROR_SERVICE_EXISTS
        !insertmacro VD_Sc 'config ${VD_SERVICE_NAME} $6'
    ${EndIf}
    ${If} $0 != 0
        MessageBox MB_ICONSTOP "Could not register the VaultDock service (error $0)." /SD IDOK
        Abort
    ${EndIf}

    !insertmacro VD_Sc 'description ${VD_SERVICE_NAME} "Runs VaultDock backups and sync in the background."'
    !insertmacro VD_Sc 'failure ${VD_SERVICE_NAME} reset= 86400 actions= restart/5000/restart/30000/restart/120000'
    !insertmacro VD_Sc 'failureflag ${VD_SERVICE_NAME} 1'
    !insertmacro VD_Sc 'sidtype ${VD_SERVICE_NAME} unrestricted'

    ${If} $7 == "advanced"
        !insertmacro VD_Sc 'privs ${VD_SERVICE_NAME} ${VD_PRIVS_ADVANCED}'
    ${Else}
        !insertmacro VD_Sc 'privs ${VD_SERVICE_NAME} ${VD_PRIVS_SIMPLE}'
    ${EndIf}
    ${If} $0 != 0
        MessageBox MB_ICONSTOP "Could not restrict the VaultDock service privileges (error $0)." /SD IDOK
        Abort
    ${EndIf}
FunctionEnd

# ------------------------------------------------------------- Uninstall --
# A macro so it can be used inside Section "uninstall" (un. functions are
# separate in NSIS). Stops and deletes the service through the agent
# (stop, wait, delete, remove the event source), with net/sc as fallback,
# closes every UI and removes the HKLM Run value. Program data is handled by
# the caller (kept unless the user ticks the box).
!macro VD_UninstallService
    StrCpy $0 "missing"
    ${If} ${FileExists} "$INSTDIR\${VD_AGENT_EXE}"
        nsExec::ExecToLog '"$INSTDIR\${VD_AGENT_EXE}" uninstall -timeout 60s'
        Pop $0
    ${EndIf}
    ${If} $0 != 0
        nsExec::ExecToLog '"$SYSDIR\net.exe" stop ${VD_SERVICE_NAME}'
        Pop $0
        nsExec::ExecToLog '"$SYSDIR\sc.exe" delete ${VD_SERVICE_NAME}'
        Pop $0
    ${EndIf}
    nsExec::Exec '"$SYSDIR\taskkill.exe" /IM "${VD_AGENT_EXE}" /F'
    Pop $0
    nsExec::Exec '"$SYSDIR\taskkill.exe" /IM "${PRODUCT_EXECUTABLE}" /F'
    Pop $0
    Sleep 800
    SetRegView 64
    DeleteRegValue HKLM "${VD_RUN_KEY}" "${VD_RUN_VALUE}"
!macroend
