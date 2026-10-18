; Goti Windows installer (NSIS 3, Modern UI 2).
;
; Build:
;   makensis -DVERSION=1.2.3 -DVERSION_NUM=1.2.3.0 -DARCH=amd64 -DSRCDIR=<dir> \
;            -DOUTFILE=<setup.exe> packaging/windows/installer.nsi
; SRCDIR must contain Goti.exe and goti-cli.exe. VERSION_NUM is the numeric
; four-part form of VERSION (pre-release suffix stripped); the package-windows
; scripts derive it. Silent install/uninstall: /S.

Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma

!ifndef VERSION
  !error "pass -DVERSION=<version>"
!endif
!ifndef VERSION_NUM
  !error "pass -DVERSION_NUM=<a.b.c.d>"
!endif
!ifndef SRCDIR
  !error "pass -DSRCDIR=<dir containing Goti.exe and goti-cli.exe>"
!endif
!ifndef ARCH
  !define ARCH amd64
!endif
!if "${ARCH}" != "amd64"
  !error "only ARCH=amd64 is supported"
!endif
!ifndef OUTFILE
  !define OUTFILE "Goti_${VERSION}_windows_${ARCH}_setup.exe"
!endif

!define APPNAME "Goti"
!define APPEXE "Goti.exe"
!define PUBLISHER "Goti"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Goti"
!define ENVKEY "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"
!define ICON "..\icons\goti.ico" ; relative to this script: makensis runs in its directory

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "x64.nsh"
!include "WinMessages.nsh"
!include "Sections.nsh"

Name "${APPNAME}"
Caption "${APPNAME} ${VERSION} Setup"
BrandingText "${APPNAME}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\${APPNAME}"
RequestExecutionLevel admin

VIProductVersion "${VERSION_NUM}"
VIFileVersion "${VERSION_NUM}"
VIAddVersionKey /LANG=1033 "ProductName" "${APPNAME}"
VIAddVersionKey /LANG=1033 "CompanyName" "${PUBLISHER}"
VIAddVersionKey /LANG=1033 "FileDescription" "${APPNAME} Setup"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "LegalCopyright" "${PUBLISHER}"
VIAddVersionKey /LANG=1033 "Comments" "Fast by design. Reliable by nature."

!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_ABORTWARNING
!define MUI_COMPONENTSPAGE_SMALLDESC
!define MUI_WELCOMEPAGE_TEXT "This will install ${APPNAME} ${VERSION} on your computer.$\r$\n$\r$\nFast by design. Reliable by nature.$\r$\n$\r$\nPlease quit ${APPNAME} before continuing. Click Next to continue."
; Launch through explorer.exe so Goti runs as the desktop user, not elevated
; (MUI_FINISHPAGE_RUN must be non-empty for the checkbox to show).
!define MUI_FINISHPAGE_RUN "$INSTDIR\${APPEXE}" ; ignored: RUN_FUNCTION below does the launch
!define MUI_FINISHPAGE_RUN_FUNCTION LaunchGoti
!define MUI_FINISHPAGE_RUN_TEXT "Run ${APPNAME}"
!define MUI_UNCONFIRMPAGE_TEXT_TOP "${APPNAME} will be removed from $INSTDIR.$\r$\nYour downloads, settings and history in %LOCALAPPDATA%\${APPNAME} are kept."

!insertmacro MUI_PAGE_WELCOME
!if /FileExists "..\..\LICENSE"
  !insertmacro MUI_PAGE_LICENSE "..\..\LICENSE"
!endif
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Var PathAdded

; ---------------------------------------------------------------------------
; Shared helpers. Each is defined twice (installer and "un." uninstaller).

; CheckNotRunning: loops while Goti.exe is running, asking the user to quit it.
; Cancel (or silent mode) aborts with exit code 2.
!macro CHECK_NOT_RUNNING_FN prefix
Function ${prefix}CheckNotRunning
  Push $0
  Push $1
  Push $2
  retry:
    nsExec::ExecToStack '"$SYSDIR\tasklist.exe" /FI "IMAGENAME eq ${APPEXE}" /NH'
    Pop $0 ; exit code
    Pop $1 ; output
    ${If} $0 != 0
      Goto done ; tasklist unavailable: do not block the user
    ${EndIf}
    Push $1
    Push "${APPEXE}"
    Call ${prefix}StrContains
    Pop $2
    ${If} $2 == 1
      MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "${APPNAME} is running. Please quit ${APPNAME} (tray icon > Quit), then click Retry." /SD IDCANCEL IDRETRY retry
      SetErrorLevel 2
      Quit
    ${EndIf}
  done:
  Pop $2
  Pop $1
  Pop $0
FunctionEnd
!macroend

; StrContains: stack (haystack, needle) -> 1 or 0. Case-insensitive.
!macro STR_CONTAINS_FN prefix
Function ${prefix}StrContains
  Exch $R1 ; needle
  Exch
  Exch $R0 ; haystack
  Push $R2
  Push $R3
  Push $R4
  StrLen $R2 $R1
  StrCpy $R3 0
  loop:
    StrCpy $R4 $R0 $R2 $R3
    StrCmp $R4 "" notfound
    StrCmp $R4 $R1 found
    IntOp $R3 $R3 + 1
    Goto loop
  found:
    StrCpy $R0 1
    Goto end
  notfound:
    StrCpy $R0 0
  end:
  Pop $R4
  Pop $R3
  Pop $R2
  Exch $R0 ; result in place of saved $R0; $R0 restored
  Exch
  Pop $R1
FunctionEnd
!macroend

; PathWithout: stack (path, entry) -> (newpath, found). Splits path on ";",
; drops empty segments and every segment equal to entry (case-insensitive,
; ignoring one trailing "\"), and reports whether entry was present.
; The result is never longer than the input.
!macro PATH_WITHOUT_FN prefix
Function ${prefix}PathWithout
  Exch $R1 ; entry
  Exch
  Exch $R0 ; path
  Push $R2 ; result
  Push $R3 ; index
  Push $R4 ; char
  Push $R5 ; segment
  Push $R6 ; found
  Push $R7 ; segment without trailing "\"
  StrCpy $R2 ""
  StrCpy $R3 0
  StrCpy $R5 ""
  StrCpy $R6 0
  loop:
    StrCpy $R4 $R0 1 $R3
    ${If} $R4 == ";"
    ${OrIf} $R4 == ""
      ${If} $R5 != ""
        StrCpy $R7 $R5
        StrCpy $R4 $R7 1 -1
        ${If} $R4 == "\"
          StrCpy $R7 $R7 -1
        ${EndIf}
        ${If} $R7 == $R1
          StrCpy $R6 1
        ${ElseIf} $R2 == ""
          StrCpy $R2 $R5
        ${Else}
          StrCpy $R2 "$R2;$R5"
        ${EndIf}
      ${EndIf}
      StrCpy $R5 ""
      StrCpy $R4 $R0 1 $R3
      StrCmp $R4 "" done
    ${Else}
      StrCpy $R5 "$R5$R4"
    ${EndIf}
    IntOp $R3 $R3 + 1
    Goto loop
  done:
  StrCpy $R0 $R2
  StrCpy $R1 $R6
  Pop $R7
  Pop $R6
  Pop $R5
  Pop $R4
  Pop $R3
  Pop $R2
  Exch $R0 ; stack: saved $R1, newpath
  Exch
  Exch $R1 ; stack: newpath, found
FunctionEnd
!macroend

; RemoveFromPath: removes $INSTDIR\bin from the machine PATH. Never writes a
; PATH it could not read in full.
!macro REMOVE_FROM_PATH_FN prefix
Function ${prefix}RemoveFromPath
  Push $0
  Push $1
  ClearErrors
  ReadRegStr $0 HKLM "${ENVKEY}" "Path"
  ${If} ${Errors}
    DetailPrint "PATH could not be read (missing or longer than ${NSIS_MAX_STRLEN} characters); left unchanged."
    Goto done
  ${EndIf}
  Push $0
  Push "$INSTDIR\bin"
  Call ${prefix}PathWithout
  Pop $1 ; found
  Pop $0 ; new path
  ${If} $1 == 1
    WriteRegExpandStr HKLM "${ENVKEY}" "Path" $0
    SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
    DetailPrint "Removed $INSTDIR\bin from PATH."
  ${EndIf}
  done:
  Pop $1
  Pop $0
FunctionEnd
!macroend

!insertmacro STR_CONTAINS_FN ""
!insertmacro STR_CONTAINS_FN "un."
!insertmacro CHECK_NOT_RUNNING_FN ""
!insertmacro CHECK_NOT_RUNNING_FN "un."
!insertmacro PATH_WITHOUT_FN ""
!insertmacro PATH_WITHOUT_FN "un."
!insertmacro REMOVE_FROM_PATH_FN "un."

; AddToPath: appends $INSTDIR\bin to the machine PATH unless already present.
; If PATH is too long to handle safely it is left untouched and the user told.
Function AddToPath
  Push $0
  Push $1
  Push $2
  Push $3
  ClearErrors
  ReadRegStr $0 HKLM "${ENVKEY}" "Path"
  ${If} ${Errors}
    MessageBox MB_OK|MB_ICONINFORMATION "Your system PATH is too long for this installer to edit safely, so it was left unchanged.$\r$\nTo use goti-cli from a terminal, add this folder to PATH yourself:$\r$\n$INSTDIR\bin" /SD IDOK
    Goto done
  ${EndIf}
  Push $0
  Push "$INSTDIR\bin"
  Call PathWithout
  Pop $1 ; found
  Pop $2 ; path without entry (unused)
  ${If} $1 == 1
    DetailPrint "$INSTDIR\bin is already on PATH."
    StrCpy $PathAdded 1
    Goto done
  ${EndIf}
  ${If} $0 == ""
    StrCpy $2 "$INSTDIR\bin"
  ${Else}
    StrCpy $3 $0 1 -1
    ${If} $3 == ";"
      StrCpy $2 "$0$INSTDIR\bin"
    ${Else}
      StrCpy $2 "$0;$INSTDIR\bin"
    ${EndIf}
  ${EndIf}
  ; NSIS silently truncates strings at NSIS_MAX_STRLEN - 1 characters.
  ; If the new value reaches that limit it may be cut, so never write it.
  StrLen $3 $2
  IntOp $1 ${NSIS_MAX_STRLEN} - 1
  ${If} $3 >= $1
    MessageBox MB_OK|MB_ICONINFORMATION "Your system PATH is too long for this installer to edit safely, so it was left unchanged.$\r$\nTo use goti-cli from a terminal, add this folder to PATH yourself:$\r$\n$INSTDIR\bin" /SD IDOK
    Goto done
  ${EndIf}
  WriteRegExpandStr HKLM "${ENVKEY}" "Path" $2
  SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
  StrCpy $PathAdded 1
  DetailPrint "Added $INSTDIR\bin to PATH."
  done:
  Pop $3
  Pop $2
  Pop $1
  Pop $0
FunctionEnd

; ---------------------------------------------------------------------------
; Installer


; Upgrade in place: run the previous version's uninstaller silently, in place
; (_?= keeps it from copying itself to %TEMP% so ExecWait really waits).
Section "-Uninstall previous version" SecUpgrade
  ReadRegStr $0 HKLM "${UNINSTKEY}" "UninstallString"
  ReadRegStr $1 HKLM "${UNINSTKEY}" "InstallLocation"
  ${If} $0 != ""
  ${AndIf} $1 != ""
    ; UninstallString is stored quoted; strip the quotes.
    StrCpy $2 $0 1
    ${If} $2 == '"'
      StrCpy $0 $0 "" 1
      StrCpy $0 $0 -1
    ${EndIf}
    ${If} ${FileExists} $0
      DetailPrint "Removing previous version from $1"
      ExecWait '"$0" /S _?=$1' $2
      DetailPrint "Previous uninstaller exited with code $2"
      ${If} $2 != 0
        MessageBox MB_OK|MB_ICONSTOP "The previous version of ${APPNAME} could not be removed (exit code $2). Setup will now stop; nothing was changed." /SD IDOK
        SetErrorLevel 2
        Abort
      ${EndIf}
      Delete $0
      RMDir $1
    ${EndIf}
  ${EndIf}
SectionEnd

Section "Goti (required)" SecMain
  SectionIn RO
  SetOutPath "$INSTDIR"
  File "${SRCDIR}/${APPEXE}"
  SetOutPath "$INSTDIR\bin"
  File "${SRCDIR}/goti-cli.exe"
  SetOutPath "$INSTDIR"

  WriteUninstaller "$INSTDIR\Uninstall.exe"

  SetShellVarContext all
  CreateShortCut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\${APPEXE}" "" "$INSTDIR\${APPEXE}" 0

  WriteRegStr HKLM "${UNINSTKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTKEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayIcon" '"$INSTDIR\${APPEXE}",0'
  WriteRegStr HKLM "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINSTKEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKLM "${UNINSTKEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoRepair" 1
SectionEnd

Section "Desktop shortcut" SecDesktop
  SetShellVarContext all
  CreateShortCut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\${APPEXE}" "" "$INSTDIR\${APPEXE}" 0
SectionEnd

Section /o "Add goti-cli to PATH" SecPath
  Call AddToPath
SectionEnd

Section "-Finish registration"
  WriteRegDWORD HKLM "${UNINSTKEY}" "AddedToPath" $PathAdded
  ; Size in KiB of the files this installer wrote, not the whole folder.
  StrCpy $0 0
  ${GetSize} "$INSTDIR" "/M=${APPEXE} /S=0K /G=0" $1 $2 $3
  IntOp $0 $0 + $1
  ${GetSize} "$INSTDIR" "/M=Uninstall.exe /S=0K /G=0" $1 $2 $3
  IntOp $0 $0 + $1
  ${GetSize} "$INSTDIR\bin" "/M=goti-cli.exe /S=0K /G=0" $1 $2 $3
  IntOp $0 $0 + $1
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "${UNINSTKEY}" "EstimatedSize" $0
SectionEnd

; .onInit comes after the sections so it can refer to ${SecPath}.
Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "${APPNAME} requires 64-bit Windows." /SD IDOK
    SetErrorLevel 2
    Quit
  ${EndIf}
  SetRegView 64
  ; Default to the previous install location when upgrading.
  ReadRegStr $0 HKLM "${UNINSTKEY}" "InstallLocation"
  ${If} $0 != ""
    StrCpy $INSTDIR $0
  ${EndIf}
  StrCpy $PathAdded 0
  ; Keep the PATH choice of the previous install (its uninstaller removes it).
  ReadRegDWORD $0 HKLM "${UNINSTKEY}" "AddedToPath"
  ${If} $0 == 1
    !insertmacro SelectSection ${SecPath}
  ${EndIf}
  Call CheckNotRunning
FunctionEnd

Function LaunchGoti
  Exec '"$WINDIR\explorer.exe" "$INSTDIR\${APPEXE}"'
FunctionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecMain} "The Goti application and the goti-cli command-line tool."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} "Put a Goti shortcut on the desktop."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecPath} "Let you run goti-cli from any terminal by adding $INSTDIR\bin to the system PATH."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

; ---------------------------------------------------------------------------
; Uninstaller. Removes only what the installer created; user data in
; %LOCALAPPDATA%\Goti is kept.

Function un.onInit
  SetRegView 64
  Call un.CheckNotRunning
FunctionEnd

Section "Uninstall"
  ; Always try: removing an entry that is not there is a no-op, and this also
  ; cleans up a PATH entry the user added by hand for this folder.
  Call un.RemoveFromPath

  SetShellVarContext all
  Delete "$SMPROGRAMS\${APPNAME}.lnk"
  Delete "$DESKTOP\${APPNAME}.lnk"

  Delete "$INSTDIR\${APPEXE}"
  Delete "$INSTDIR\bin\goti-cli.exe"
  RMDir "$INSTDIR\bin"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"

  DeleteRegKey HKLM "${UNINSTKEY}"
SectionEnd
