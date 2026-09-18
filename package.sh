#!/usr/bin/env bash
# Builds distributable packages into dist/. Connections are NEVER included: they live in each
# user's ~/.config/durusql, the packages only contain the binary, a desktop entry and an icon.
#
#   ./package.sh [version]        Linux: dist/durusql_<ver>_amd64.deb + dist/durusql-<ver>-linux-amd64.tar.gz
#                                 macOS (run on a Mac): dist/durusql-<ver>-macos.zip (durusql.app, universal)
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; cd "$ROOT"
VERSION="${1:-$(cat VERSION 2>/dev/null || echo 0.1.0)}"
CHANNEL="${2:-$( [[ "$VERSION" == *-* ]] && echo beta || echo stable )}"   # 0.6.0-beta.1 → beta
UPDATE_SOURCE="${DURUSQL_UPDATE_SOURCE:-$(cat UPDATE_SOURCE 2>/dev/null || true)}"   # e.g. https://github.com/makbulut/durusql
LD="-X main.version=$VERSION -X main.updateSource=$UPDATE_SOURCE"
ARCH="$(dpkg --print-architecture 2>/dev/null || uname -m)"
export PATH="$HOME/go/bin:$PATH"
rm -rf dist; mkdir -p dist

if [ "$(uname)" = "Darwin" ]; then
  # Prerequisites: Xcode command line tools (xcode-select --install), Go, Node, Wails CLI.
  command -v go >/dev/null   || { echo "install Go first:   brew install go"; exit 1; }
  command -v npm >/dev/null  || { echo "install Node first: brew install node"; exit 1; }
  command -v wails >/dev/null || go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
  (cd frontend && npm install --no-audit --no-fund >/dev/null)
  wails build -clean -platform darwin/universal -ldflags "$LD"
  # ad-hoc signature: Apple Silicon refuses to run unsigned binaries at all
  codesign --force --deep -s - build/bin/durusql.app
  (cd build/bin && zip -qry "$ROOT/dist/durusql-$VERSION-macos.zip" durusql.app)
  if command -v hdiutil >/dev/null; then
    rm -rf dist/dmg && mkdir -p dist/dmg && cp -R build/bin/durusql.app dist/dmg/ && ln -s /Applications dist/dmg/Applications
    hdiutil create -quiet -volname "DuruSQL $VERSION" -srcfolder dist/dmg -ov -format UDZO "dist/durusql-$VERSION-macos.dmg" && rm -rf dist/dmg
  fi
  ls -la dist | awk 'NR>1{print $5, $9}'
  echo
  echo "Install: open the .dmg (or unzip) and drag durusql.app to Applications."
  echo "First start (not notarized): right-click durusql.app → Open → Open. If macOS says the app is damaged:"
  echo "  xattr -dr com.apple.quarantine /Applications/durusql.app"
  echo "Dumps/restores: brew install mysql-client libpq   (then add their bin dirs to PATH, brew prints how)"
  exit 0
fi

# ---- Linux build (needs the same toolchain as ./run.sh; run.sh installs it) ----
./run.sh build >/dev/null
[ -x build/bin/durusql ] || { echo "build failed"; exit 1; }
strip build/bin/durusql 2>/dev/null || true

# ---- .deb ----
DEB="dist/deb"; rm -rf "$DEB"
mkdir -p "$DEB/DEBIAN" "$DEB/usr/bin" "$DEB/usr/share/applications" "$DEB/usr/share/icons/hicolor/scalable/apps" "$DEB/usr/share/doc/durusql"
install -m 755 build/bin/durusql "$DEB/usr/bin/durusql"
install -m 644 packaging/durusql.desktop "$DEB/usr/share/applications/durusql.desktop"
install -m 644 packaging/durusql.svg "$DEB/usr/share/icons/hicolor/scalable/apps/durusql.svg"
install -m 644 README.md "$DEB/usr/share/doc/durusql/README.md"
SIZE=$(du -sk "$DEB/usr" | cut -f1)
cat > "$DEB/DEBIAN/control" <<CTRL
Package: durusql
Version: $VERSION
Section: database
Priority: optional
Architecture: $ARCH
Installed-Size: $SIZE
Depends: libwebkit2gtk-4.1-0, libgtk-3-0
Recommends: mariadb-client | mysql-client, postgresql-client
Maintainer: Mehmet Akbulut <mehmet.akbulut@iceshop.nl>
Homepage: https://iceshop.nl
Description: Lightweight DataGrip-style database client
 MySQL/MariaDB and PostgreSQL client with SSH tunnels, editable grids,
 console tabs, saved queries, dumps and restores. Connections are stored
 per user in ~/.config/durusql.
CTRL
dpkg-deb --build --root-owner-group "$DEB" "dist/durusql_${VERSION}_${ARCH}.deb" >/dev/null
rm -rf "$DEB"

# ---- portable tar.gz ----
TAR="dist/durusql-$VERSION-linux-$ARCH"; rm -rf "$TAR"; mkdir -p "$TAR"
cp build/bin/durusql packaging/durusql.desktop packaging/durusql.svg packaging/install.sh README.md "$TAR/"
tar -C dist -czf "$TAR.tar.gz" "$(basename "$TAR")"; rm -rf "$TAR"

# ---- Windows (cross-compiled: WebView2, no C toolchain needed) ----
wails build -platform windows/amd64 -ldflags "$LD" -o durusql.exe >/dev/null 2>&1 || echo "windows build failed (see wails output)"
if [ -f build/bin/durusql.exe ]; then
  WIN="dist/durusql-$VERSION-windows-amd64"; rm -rf "$WIN"; mkdir -p "$WIN"
  cp build/bin/durusql.exe README.md "$WIN/"
  (cd dist && zip -qr "$(basename "$WIN").zip" "$(basename "$WIN")"); rm -rf "$WIN"
  if command -v makensis >/dev/null; then
    wails build -platform windows/amd64 -nsis -ldflags "$LD" >/dev/null 2>&1 && cp build/bin/*-installer.exe dist/ 2>/dev/null || true
  fi
fi

# ---- checksums + channel document (for self-hosted JSON channels; GitHub uses SHA256SUMS + the API) ----
(cd dist && shopt -s nullglob && sha256sum *.deb *.tar.gz *.zip *.exe *.dmg > SHA256SUMS)
NOTES=$(awk -v v="$VERSION" '/^## /{ if (found) exit; if (index($0, v)) { found=1; next } } found' CHANGELOG.md 2>/dev/null || true)
python3 - "$VERSION" "$CHANNEL" "$UPDATE_SOURCE" "$NOTES" <<'PY'
import json, os, sys, hashlib, glob
ver, channel, base, notes = sys.argv[1:5]
kinds = {'.deb': 'linux-deb', 'linux-amd64.tar.gz': 'linux-tar', 'windows-amd64.zip': 'windows-zip', 'installer.exe': 'windows-installer', '.dmg': 'macos-dmg', 'macos.zip': 'macos-zip'}
files = {}
for f in sorted(glob.glob('dist/*')):
    name = os.path.basename(f)
    kind = next((k for suf, k in kinds.items() if name.endswith(suf)), None)
    if not kind: continue
    url = (base.rstrip('/') + '/releases/download/v' + ver + '/' + name) if 'github.com' in base else (base.rstrip('/') + '/' + name if base else name)
    files[kind] = {'url': url, 'sha256': hashlib.sha256(open(f, 'rb').read()).hexdigest(), 'size': os.path.getsize(f)}
doc = {'version': ver, 'date': __import__('datetime').date.today().isoformat(), 'notes': notes.strip(), 'files': files}
json.dump(doc, open(f'dist/{channel}.json', 'w'), indent=2)
print(f'→ dist/{channel}.json ({len(files)} packages)')
PY

ls -la dist | awk 'NR>1{print $5, $9}'
echo
echo "Release: ./publish.sh $VERSION      (GitHub release via gh, pre-release when the version has a suffix like -beta.1)"
echo "Install on another Linux PC:"
echo "  sudo apt install ./durusql_${VERSION}_${ARCH}.deb          (Ubuntu 22.04+/Debian 12+, pulls libwebkit2gtk-4.1)"
echo "  or: tar xzf durusql-$VERSION-linux-$ARCH.tar.gz && ./durusql-$VERSION-linux-$ARCH/install.sh   (per-user, no root)"
echo "Windows: unzip durusql-$VERSION-windows-amd64.zip and run durusql.exe (needs the WebView2 runtime, present on Windows 10/11; it offers to download it otherwise)."
echo "macOS: clone the repo on a Mac, install Go + Node + Wails, then run ./package.sh (Wails cannot cross-compile macOS from Linux)."
