; SmartTube for Windows - installer
; Build: makensis -DVERSION=1.0.0 installer/smarttube.nsi

Unicode true
!ifndef VERSION
  !define VERSION "1.0.0"
!endif
!define APPNAME "SmartTube"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\SmartTubeWindows"

Name "${APPNAME}"
OutFile "..\dist\SmartTube-Setup-${VERSION}.exe"
InstallDir "$LOCALAPPDATA\Programs\SmartTube"
InstallDirRegKey HKCU "Software\SmartTubeWindows" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
BrandingText "SmartTube for Windows ${VERSION}"
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "SmartTube for Windows"
VIAddVersionKey "FileDescription" "SmartTube for Windows Setup"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "SmartTube (c) yuliskov, MIT License"

!include "MUI2.nsh"
!define MUI_ICON "..\assets\smarttube.ico"
!define MUI_UNICON "..\assets\smarttube.ico"
!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "SmartTube for Windows"
!define MUI_WELCOMEPAGE_TEXT "This installs SmartTube, the ad-free YouTube client for Android TV, on Windows.$\r$\n$\r$\nSmartTube runs unmodified inside a lightweight Android TV runtime (Google's official Android Emulator). On first setup about 1.5 GB is downloaded from Google and GitHub.$\r$\n$\r$\nEvery time you open SmartTube it checks GitHub for the newest SmartTube release and installs it automatically.$\r$\n$\r$\nRequires 64-bit Windows 10/11 with virtualization enabled."
!define MUI_FINISHPAGE_RUN "$INSTDIR\SmartTube.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open SmartTube now"
!define MUI_COMPONENTSPAGE_SMALLDESC

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "..\LICENSE-SmartTube.txt"
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "SmartTube (required)" SecMain
  SectionIn RO
  ; stop a running copy so the launcher can be replaced
  nsExec::Exec 'taskkill /IM SmartTube.exe /F'
  SetOutPath "$INSTDIR"
  File "..\dist\SmartTube.exe"
  File "..\assets\smarttube.ico"
  File "..\README.md"
  File "..\LICENSE-SmartTube.txt"

  CreateDirectory "$SMPROGRAMS\SmartTube"
  CreateShortcut "$SMPROGRAMS\SmartTube\SmartTube.lnk" "$INSTDIR\SmartTube.exe" "" "$INSTDIR\smarttube.ico"
  CreateShortcut "$SMPROGRAMS\SmartTube\Update SmartTube now.lnk" "$INSTDIR\SmartTube.exe" "--update" "$INSTDIR\smarttube.ico"
  CreateShortcut "$SMPROGRAMS\SmartTube\Repair Android runtime.lnk" "$INSTDIR\SmartTube.exe" "--repair" "$INSTDIR\smarttube.ico"
  CreateShortcut "$SMPROGRAMS\SmartTube\Reset SmartTube.lnk" "$INSTDIR\SmartTube.exe" "--reset" "$INSTDIR\smarttube.ico"
  CreateShortcut "$SMPROGRAMS\SmartTube\Settings (config.json).lnk" "notepad.exe" "$LOCALAPPDATA\SmartTubeWin\config.json"
  CreateShortcut "$SMPROGRAMS\SmartTube\Log file.lnk" "notepad.exe" "$LOCALAPPDATA\SmartTubeWin\launcher.log"
  CreateShortcut "$SMPROGRAMS\SmartTube\Controller test.lnk" "$INSTDIR\SmartTube.exe" "--gamepad-test" "$INSTDIR\smarttube.ico"
  CreateShortcut "$SMPROGRAMS\SmartTube\Uninstall SmartTube.lnk" "$INSTDIR\Uninstall.exe"

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "Software\SmartTubeWindows" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "SmartTube for Windows"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "Publisher" "SmartTube for Windows (unofficial)"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\smarttube.ico"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "URLInfoAbout" "https://github.com/yuliskov/SmartTube"
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
SectionEnd

Section "Desktop shortcut" SecDesktop
  CreateShortcut "$DESKTOP\SmartTube.lnk" "$INSTDIR\SmartTube.exe" "" "$INSTDIR\smarttube.ico"
SectionEnd

Section "Download Android TV runtime now (recommended)" SecRuntime
  DetailPrint "Downloading the Android TV runtime (about 1.5 GB). Progress is shown in the SmartTube setup window..."
  ExecWait '"$INSTDIR\SmartTube.exe" --setup' $0
  DetailPrint "Runtime setup finished (code $0)."
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecMain} "The SmartTube launcher and Start-menu shortcuts."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} "Put a SmartTube icon on the desktop."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecRuntime} "Download Google's Android Emulator and an Android TV image now. If unchecked, this happens the first time you open SmartTube."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  nsExec::Exec 'taskkill /IM SmartTube.exe /F'
  MessageBox MB_YESNO|MB_ICONQUESTION "Also delete the Android TV runtime and SmartTube data (settings, sign-in, about 3-6 GB)?" IDNO keepdata
    ExecWait '"$INSTDIR\SmartTube.exe" --purge'
    RMDir /r "$LOCALAPPDATA\SmartTubeWin"
  keepdata:
  Delete "$DESKTOP\SmartTube.lnk"
  RMDir /r "$SMPROGRAMS\SmartTube"
  Delete "$INSTDIR\SmartTube.exe"
  Delete "$INSTDIR\smarttube.ico"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\LICENSE-SmartTube.txt"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINSTKEY}"
  DeleteRegKey HKCU "Software\SmartTubeWindows"
SectionEnd
