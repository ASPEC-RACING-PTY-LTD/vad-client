####
## VaultDock install modes (MASTER_PLAN section 7.7, R11): command line, mode page,
## (i) info "dialogs" and the mode file. Productised from spike P0-S5
## (planning/spikes/installer-modes/vaultdock_modes.nsh).
##
## Needs (from project.nsi / common.nsh): MUI2, LogicLib, nsDialogs, FileFunc,
## the variables VD_DataDir / VD_DataTrusted and the helper VD_StrContains.
##
## The (i) texts are the single source of truth in modes\*.en.txt (P1-31):
## plain ASCII, LF line endings. They are compiled into the installer and
## loaded line by line, so NSIS_MAX_STRLEN does not limit their length.
####

!ifndef VD_MODES_DIR
    !define VD_MODES_DIR "modes"
!endif
# Relative to %ProgramData%\VaultDock. Read by the agent's module manager
# (desktop/internal/modules, P0-38) on first start; state.db is the
# authority afterwards and the manager keeps this file in sync.
!define VD_MODE_FILE "config\mode.json"

# The (i) glyph. ${U+24D8} needs NSIS 3; set to "(i)" if a build tool
# renders it badly.
!ifndef VD_INFO_GLYPH
    !define VD_INFO_GLYPH "${U+24D8}"
!endif

!ifndef EM_REPLACESEL
    !define EM_REPLACESEL 0x00C2
!endif
!ifndef EM_LIMITTEXT
    !define EM_LIMITTEXT 0x00C5
!endif
!ifndef ES_AUTOVSCROLL
    !define ES_AUTOVSCROLL 0x0040
!endif
!ifndef ES_READONLY
    !define ES_READONLY 0x0800
!endif
!define VD_WM_NOTIFY_OUTER_NEXT 0x408

Var VD_Mode          ; "simple" | "advanced" to write; "" = keep the existing mode
Var VD_ExistingMode  ; mode read from an existing trusted mode file ("" = none)
Var VD_ModeArg       ; value of /MODE= ("" = not given)
Var VD_SkipModePage  ; "1" = the mode page is not shown
Var VD_InfoWhich     ; "" | "simple" | "advanced": which (i) page to show
Var VD_RadioSimple
Var VD_RadioAdvanced
Var VD_InfoEdit

# ------------------------------------------------------------ .onInit part --
# Call after VD_DataDir and VD_DataTrusted are set.
#   /MODE=simple|advanced  chooses the mode for a fresh install and skips the
#                          page; any other value exits with error level 2.
#   Existing install (upgrade, or a reinstall over data kept by an earlier
#   uninstall): the existing mode is kept and the page is skipped. The mode is
#   changed in Settings > Mode only (R11); /MODE= is then ignored.
#   Fresh silent install without /MODE: simple.
Function VD_ModeInit
    StrCpy $VD_InfoWhich ""
    StrCpy $VD_ModeArg ""
    ${GetParameters} $R0
    ClearErrors
    ${GetOptions} $R0 "/MODE=" $R1
    ${IfNot} ${Errors}
        ${If} $R1 == "simple"
            StrCpy $VD_ModeArg "simple"
        ${ElseIf} $R1 == "advanced"
            StrCpy $VD_ModeArg "advanced"
        ${Else}
            MessageBox MB_ICONSTOP "Unknown /MODE value '$R1'. Use /MODE=simple or /MODE=advanced." /SD IDOK
            SetErrorLevel 2
            Quit
        ${EndIf}
    ${EndIf}

    StrCpy $VD_ExistingMode ""
    ${If} $VD_DataTrusted == "1"
        Call VD_ReadModeFile
    ${EndIf}

    ${If} $VD_ExistingMode != ""
        StrCpy $VD_Mode ""
        StrCpy $VD_SkipModePage "1"
    ${ElseIf} $VD_ModeArg != ""
        StrCpy $VD_Mode $VD_ModeArg
        StrCpy $VD_SkipModePage "1"
    ${Else}
        StrCpy $VD_Mode "simple"
        StrCpy $VD_SkipModePage "0"
    ${EndIf}
FunctionEnd

# Sets VD_ExistingMode from %ProgramData%\VaultDock\config\mode.json.
# The file is {"mode":"simple"} (agent) or {"version":1,"mode":...} (this
# installer); only the mode value can be the quoted word "advanced"/"simple".
Function VD_ReadModeFile
    StrCpy $VD_ExistingMode ""
    ClearErrors
    FileOpen $0 "$VD_DataDir\${VD_MODE_FILE}" r
    ${If} ${Errors}
        Return
    ${EndIf}
    FileRead $0 $1
    FileClose $0
    StrCpy $R8 $1
    StrCpy $R9 '"advanced"'
    Call VD_StrContains
    ${If} $R7 == "1"
        StrCpy $VD_ExistingMode "advanced"
        Return
    ${EndIf}
    StrCpy $R9 '"simple"'
    Call VD_StrContains
    ${If} $R7 == "1"
        StrCpy $VD_ExistingMode "simple"
    ${EndIf}
FunctionEnd

# -------------------------------------------------------------- Mode page --
Function VD_ModePageCreate
    ${If} $VD_SkipModePage == "1"
        Abort
    ${EndIf}
    StrCpy $VD_InfoWhich ""

    InitPluginsDir
    File "/oname=$PLUGINSDIR\vd-simple.txt" "${VD_MODES_DIR}\simple.en.txt"
    File "/oname=$PLUGINSDIR\vd-advanced.txt" "${VD_MODES_DIR}\advanced.en.txt"

    !insertmacro MUI_HEADER_TEXT "Choose how you want to use VaultDock" \
        "You can switch later in Settings without reinstalling or losing backups."

    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
        Abort
    ${EndIf}

    ${NSD_CreateRadioButton} 0 0 68% 12u "&Simple Folder Backups (recommended)"
    Pop $VD_RadioSimple
    ${NSD_AddStyle} $VD_RadioSimple ${WS_GROUP}
    ${NSD_CreateLink} 70% 1u 30% 10u "${VD_INFO_GLYPH} What does this include?"
    Pop $0
    ${NSD_OnClick} $0 VD_OnInfoSimple
    ${NSD_CreateLabel} 12u 14u -12u 28u "Pick folders in your profile, back them up automatically and restore files. Smallest download, fewest permissions."
    Pop $0

    ${NSD_CreateRadioButton} 0 50u 68% 12u "&Full Access to All Tools (Advanced)"
    Pop $VD_RadioAdvanced
    ${NSD_CreateLink} 70% 51u 30% 10u "${VD_INFO_GLYPH} What does this include?"
    Pop $0
    ${NSD_OnClick} $0 VD_OnInfoAdvanced
    ${NSD_CreateLabel} 12u 64u -12u 36u "Everything in Simple plus sync, automations, app discovery and reinstall, settings backup, snapshots of locked files and multi-profile protection. Downloads extra components after installation."
    Pop $0

    ${NSD_CreateLabel} 0 108u 100% 24u "Both modes install a background service, which needs administrator rights. Backups read files with each user's own Windows permissions."
    Pop $0

    ${If} $VD_Mode == "advanced"
        ${NSD_Check} $VD_RadioAdvanced
    ${Else}
        ${NSD_Check} $VD_RadioSimple
    ${EndIf}

    nsDialogs::Show
FunctionEnd

Function VD_ModePageLeave
    ${NSD_GetState} $VD_RadioAdvanced $0
    ${If} $0 == ${BST_CHECKED}
        StrCpy $VD_Mode "advanced"
    ${Else}
        StrCpy $VD_Mode "simple"
    ${EndIf}
FunctionEnd

# The (i) links move one page forward (the leave function above keeps the
# radio choice); the info page then shows the text.
Function VD_OnInfoSimple
    Pop $0
    StrCpy $VD_InfoWhich "simple"
    SendMessage $HWNDPARENT ${VD_WM_NOTIFY_OUTER_NEXT} 1 0
FunctionEnd

Function VD_OnInfoAdvanced
    Pop $0
    StrCpy $VD_InfoWhich "advanced"
    SendMessage $HWNDPARENT ${VD_WM_NOTIFY_OUTER_NEXT} 1 0
FunctionEnd

# ----------------------------------------------------- (i) info "dialog" --
# nsDialogs cannot open a second modal window without a third-party plugin,
# so the "dialog" is a wizard page that behaves like one: it appears only
# after an (i) link was clicked, hides Next, and Back returns to the mode
# page with the selection kept. Otherwise it is skipped in both directions.
Function VD_InfoPageCreate
    ${If} $VD_InfoWhich == ""
        Abort
    ${EndIf}

    ${If} $VD_InfoWhich == "advanced"
        !insertmacro MUI_HEADER_TEXT "Full Access to All Tools (Advanced)" "What this mode does, what it needs and how it differs."
    ${Else}
        !insertmacro MUI_HEADER_TEXT "Simple Folder Backups" "What this mode does, what it needs and how it differs."
    ${EndIf}

    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
        Abort
    ${EndIf}

    nsDialogs::CreateControl EDIT ${DEFAULT_STYLES}|${WS_TABSTOP}|${WS_VSCROLL}|${ES_MULTILINE}|${ES_AUTOVSCROLL}|${ES_READONLY} ${WS_EX_CLIENTEDGE} 0 0 100% 100% ""
    Pop $VD_InfoEdit
    SendMessage $VD_InfoEdit ${EM_LIMITTEXT} 0 0
    Call VD_LoadInfoText

    GetDlgItem $0 $HWNDPARENT 1          ; Next
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 3          ; Back
    SendMessage $0 ${WM_SETTEXT} 0 "STR:< Back to the choice"

    nsDialogs::Show

    # Page left (Back or Cancel): restore the wizard buttons.
    GetDlgItem $0 $HWNDPARENT 1
    ShowWindow $0 ${SW_SHOW}
    GetDlgItem $0 $HWNDPARENT 3
    SendMessage $0 ${WM_SETTEXT} 0 "STR:$(^BackBtn)"
    StrCpy $VD_InfoWhich ""
FunctionEnd

# Appends the text file to the edit control line by line (LF -> CRLF).
Function VD_LoadInfoText
    ClearErrors
    FileOpen $1 "$PLUGINSDIR\vd-$VD_InfoWhich.txt" r
    ${If} ${Errors}
        SendMessage $VD_InfoEdit ${WM_SETTEXT} 0 "STR:The description could not be loaded. See Settings > Mode after installation."
        Return
    ${EndIf}
    ${Do}
        ClearErrors
        FileRead $1 $2
        ${If} ${Errors}
            ${Break}
        ${EndIf}
        StrCpy $3 $2 1 -1
        ${If} $3 == "$\n"
            StrCpy $2 $2 -1
            StrCpy $3 $2 1 -1
            ${If} $3 == "$\r"
                StrCpy $2 $2 -1
            ${EndIf}
            StrCpy $2 "$2$\r$\n"
        ${EndIf}
        SendMessage $VD_InfoEdit ${WM_GETTEXTLENGTH} 0 0 $4
        SendMessage $VD_InfoEdit ${EM_SETSEL} $4 $4
        SendMessage $VD_InfoEdit ${EM_REPLACESEL} 0 "STR:$2"
    ${Loop}
    FileClose $1
    SendMessage $VD_InfoEdit ${EM_SETSEL} 0 0
FunctionEnd

# --------------------------------------------------------------- Install --
# Writes config\mode.json for a fresh install (VD_Mode set). The file
# inherits the config folder's ACL (SYSTEM/Administrators full, Users read).
Function VD_WriteModeFile
    ${If} $VD_Mode == ""
        ${If} $VD_ModeArg != ""
        ${AndIf} $VD_ModeArg != $VD_ExistingMode
            DetailPrint "/MODE=$VD_ModeArg ignored: VaultDock is already installed in $VD_ExistingMode mode. Change it in Settings > Mode."
        ${EndIf}
        DetailPrint "Keeping the existing install mode ($VD_ExistingMode)."
        Return
    ${EndIf}
    StrCpy $5 "$VD_DataDir\${VD_MODE_FILE}"
    Delete "$5.tmp"
    ClearErrors
    FileOpen $0 "$5.tmp" w
    ${If} ${Errors}
        MessageBox MB_ICONSTOP "Could not write $5." /SD IDOK
        Abort
    ${EndIf}
    FileWrite $0 '{"version":1,"mode":"$VD_Mode","source":"installer","installer_version":"${INFO_PRODUCTVERSION}"}$\r$\n'
    FileClose $0
    Delete "$5"
    ClearErrors
    Rename "$5.tmp" "$5"
    ${If} ${Errors}
        MessageBox MB_ICONSTOP "Could not write $5." /SD IDOK
        Abort
    ${EndIf}
    DetailPrint "Install mode: $VD_Mode"
FunctionEnd

# Mode that decides the service privileges: the new choice, else the kept one.
# Result in $R0.
Function VD_EffectiveMode
    StrCpy $R0 $VD_Mode
    ${If} $R0 == ""
        StrCpy $R0 $VD_ExistingMode
    ${EndIf}
    ${If} $R0 != "advanced"
        StrCpy $R0 "simple"
    ${EndIf}
FunctionEnd
