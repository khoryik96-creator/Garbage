Unicode true
!include "MUI2.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!ifndef VERSION
  !error "VERSION is required"
!endif
Name "Garbage Truck"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\Garbage Truck"
InstallDirRegKey HKCU "Software\GarbageTruck" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
Icon "${ICON_FILE}"
UninstallIcon "${ICON_FILE}"
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "Garbage Truck"
VIAddVersionKey "FileDescription" "Garbage Truck desktop installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "Garbage Truck contributors"
!define MUI_ABORTWARNING
!define MUI_CUSTOMFUNCTION_ABORT CleanupOnAbort
!define MUI_WELCOMEPAGE_TITLE "Welcome to Garbage Truck"
!define MUI_WELCOMEPAGE_TEXT "A simpler workspace for candidate coding.$\r$\n$\r$\nSetup installs the app and a shortcut for your Windows account. Go and Python are not required.$\r$\n$\r$\nThis version uses demo profiles. Your workspace is kept when you upgrade or uninstall."
!define MUI_FINISHPAGE_RUN "$INSTDIR\Garbage Truck.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open Garbage Truck"
!define MUI_FINISHPAGE_TEXT "Your app is ready. Use the Garbage Truck shortcut in the Start menu or on your desktop.$\r$\n$\r$\nThe interface opens in your browser. Use Quit Garbage Truck in the app when you are done."
!ifdef SIGN_SCRIPT
  !uninstfinalize 'powershell.exe -NoProfile -ExecutionPolicy Bypass -File "${SIGN_SCRIPT}" -Target "%1"' = 0
!endif
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
Var StageDirectory

Function RecoverExecutable
  IfFileExists "$INSTDIR\Garbage Truck.previous.exe" 0 recovered
  System::Call 'kernel32::MoveFileExW(w "$INSTDIR\Garbage Truck.previous.exe", w "$INSTDIR\Garbage Truck.exe", i 9) i.r1'
  IntCmp $1 0 recovery_failed recovered recovered
  recovery_failed:
  SetErrorLevel 1
  Abort
  recovered:
FunctionEnd

Function CleanupStage
  StrCmp $StageDirectory "" cleanup_done
  SetOutPath "$INSTDIR"
  RMDir /r "$StageDirectory"
  cleanup_done:
FunctionEnd

Function .onInstFailed
  !ifdef TEST_DENY_EXTRACTION
    FileClose $9
  !endif
  Call RecoverExecutable
  Call CleanupStage
FunctionEnd

Function CleanupOnAbort
  Call RecoverExecutable
  Call CleanupStage
FunctionEnd

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "Garbage Truck needs a 64-bit Windows 10 or 11 computer."
    Abort
  ${EndIf}
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_OK|MB_ICONSTOP "Garbage Truck needs Windows 10 or 11."
    Abort
  ${EndIf}
FunctionEnd

!macro CheckClosed
  IfFileExists "$LOCALAPPDATA\GarbageTruck\app.lock" 0 ready
  retry:
  ClearErrors
  FileOpen $0 "$LOCALAPPDATA\GarbageTruck\app.lock" a
  IfErrors busy
  FileClose $0
  Goto ready
  busy:
  IfSilent abort
  MessageBox MB_RETRYCANCEL|MB_ICONINFORMATION "Quit Garbage Truck from its app menu before installing or upgrading, then click Retry. Your work is saved automatically." IDRETRY retry
  abort:
  SetErrorLevel 1
  Abort
  ready:
!macroend
Function CheckClosed
  !insertmacro CheckClosed
FunctionEnd
Function un.CheckClosed
  !insertmacro CheckClosed
FunctionEnd

Section "Install"
  Call CheckClosed
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  ; Recover the previous executable left by an interrupted older installer.
  ; Replacement uses a same-volume atomic rename: a killed installer always
  ; leaves either the old or new executable at its normal launch path.
  Call RecoverExecutable
  StrCpy $StageDirectory "$INSTDIR\.garbage-update"
  Call CleanupStage
  ClearErrors
  CreateDirectory "$StageDirectory"
  IfErrors install_failed
  SetOutPath "$StageDirectory"
  !ifdef TEST_DENY_EXTRACTION
    ; Test build only: provoke NSIS's actual extraction-abort path.
    FileOpen $9 "$StageDirectory\Garbage Truck.exe" w
  !endif
  File "/oname=Garbage Truck.exe" "${SOURCE_EXE}"
  File /oname=app.ico "${ICON_FILE}"
  WriteUninstaller "$StageDirectory\Uninstall.exe"
  IfErrors install_failed
  IfFileExists "$INSTDIR\Garbage Truck.exe" 0 replace_app
  System::Call 'kernel32::CopyFileW(w "$INSTDIR\Garbage Truck.exe", w "$StageDirectory\previous.exe", i 0) i.r1'
  IntCmp $1 0 install_failed
  System::Call 'kernel32::MoveFileExW(w "$StageDirectory\previous.exe", w "$INSTDIR\Garbage Truck.previous.exe", i 9) i.r1'
  IntCmp $1 0 install_failed
  !ifdef TEST_INTERRUPT_AFTER_BACKUP
    System::Call 'kernel32::TerminateProcess(p -1, i 91)'
  !endif
  replace_app:
  System::Call 'kernel32::MoveFileExW(w "$StageDirectory\Garbage Truck.exe", w "$INSTDIR\Garbage Truck.exe", i 9) i.r1'
  IntCmp $1 0 install_failed
  !ifdef TEST_INTERRUPT_AFTER_REPLACE
    System::Call 'kernel32::TerminateProcess(p -1, i 92)'
  !endif
  System::Call 'kernel32::MoveFileExW(w "$StageDirectory\app.ico", w "$INSTDIR\app.ico", i 9) i.r1'
  IntCmp $1 0 install_failed
  System::Call 'kernel32::MoveFileExW(w "$StageDirectory\Uninstall.exe", w "$INSTDIR\Uninstall.exe", i 9) i.r1'
  IntCmp $1 0 install_failed
  SetOutPath "$INSTDIR"
  CreateDirectory "$SMPROGRAMS\Garbage Truck"
  CreateShortcut "$SMPROGRAMS\Garbage Truck\Garbage Truck.lnk" "$INSTDIR\Garbage Truck.exe" "" "$INSTDIR\app.ico"
  CreateShortcut "$SMPROGRAMS\Garbage Truck\Open interface again.lnk" "$INSTDIR\Garbage Truck.exe" "" "$INSTDIR\app.ico"
  CreateShortcut "$DESKTOP\Garbage Truck.lnk" "$INSTDIR\Garbage Truck.exe" "" "$INSTDIR\app.ico"
  WriteRegStr HKCU "Software\GarbageTruck" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "DisplayName" "Garbage Truck"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "DisplayIcon" "$INSTDIR\app.ico"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "Publisher" "Garbage Truck contributors"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "URLUpdateInfo" "https://github.com/khoryik96-creator/Garbage/releases"
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck" "NoRepair" 1
  Delete "$INSTDIR\Garbage Truck.previous.exe"
  Call CleanupStage
  Goto install_done
  install_failed:
  Call RecoverExecutable
  Call CleanupStage
  IfSilent +2
  MessageBox MB_OK|MB_ICONSTOP "Setup could not update the app. Close Garbage Truck and try again. Your saved workspace has been kept."
  SetErrorLevel 1
  Abort
  install_done:
SectionEnd

Section "Uninstall"
  Call un.CheckClosed
  SetShellVarContext current
  Delete "$DESKTOP\Garbage Truck.lnk"
  Delete "$SMPROGRAMS\Garbage Truck\Garbage Truck.lnk"
  Delete "$SMPROGRAMS\Garbage Truck\Open interface again.lnk"
  RMDir "$SMPROGRAMS\Garbage Truck"
  Delete "$INSTDIR\Garbage Truck.exe"
  Delete "$INSTDIR\Garbage Truck.previous.exe"
  RMDir /r "$INSTDIR\.garbage-update"
  Delete "$INSTDIR\app.ico"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "Software\GarbageTruck"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\GarbageTruck"
  ; Deliberately retain $LOCALAPPDATA\GarbageTruck, which owns the user's database.
SectionEnd
