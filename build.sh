#!/usr/bin/env bash
# Builds dist/SmartTube.exe and dist/SmartTube-Setup-<version>.exe
# Needs: go, makensis (nsis), x86_64-w64-mingw32-windres (binutils-mingw-w64)
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-$(cat VERSION)}"
IFS=. read -r MA MI PA <<<"$VERSION"
mkdir -p dist
cat > launcher/res/app.rc <<RC

1 ICON "../../assets/smarttube.ico"
1 24 "app.manifest"
1 VERSIONINFO
FILEVERSION $MA,$MI,${PA:-0},0
PRODUCTVERSION $MA,$MI,${PA:-0},0
BEGIN
  BLOCK "StringFileInfo"
  BEGIN
    BLOCK "040904b0"
    BEGIN
      VALUE "CompanyName", "SmartTube for Windows (unofficial)"
      VALUE "FileDescription", "SmartTube"
      VALUE "FileVersion", "$VERSION"
      VALUE "ProductName", "SmartTube for Windows"
      VALUE "ProductVersion", "$VERSION"
      VALUE "OriginalFilename", "SmartTube.exe"
    END
  END
  BLOCK "VarFileInfo"
  BEGIN
    VALUE "Translation", 0x409, 1200
  END
END
RC
(cd launcher/res && x86_64-w64-mingw32-windres --preprocessor=cpp --preprocessor-arg=-P -O coff -i app.rc -o ../rsrc_windows_amd64.syso)
(cd launcher && go test ./... && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=$VERSION" -o ../dist/SmartTube.exe .)
makensis -V2 -DVERSION="$VERSION" installer/smarttube.nsi
ls -la dist
