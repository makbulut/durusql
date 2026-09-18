#!/usr/bin/env bash
# Installs the portable durusql bundle for the current user (no root needed).
set -e
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$HOME/.local/bin"; APPS="$HOME/.local/share/applications"; ICONS="$HOME/.local/share/icons/hicolor/scalable/apps"
mkdir -p "$BIN" "$APPS" "$ICONS"
install -m 755 "$HERE/durusql" "$BIN/durusql"
install -m 644 "$HERE/durusql.svg" "$ICONS/durusql.svg"
sed "s|^Exec=.*|Exec=$BIN/durusql|" "$HERE/durusql.desktop" > "$APPS/durusql.desktop"
command -v update-desktop-database >/dev/null && update-desktop-database "$APPS" 2>/dev/null || true
missing=""
for lib in libwebkit2gtk-4.1.so.0 libgtk-3.so.0; do ldconfig -p 2>/dev/null | grep -q "$lib" || missing="$missing $lib"; done
if [ -n "$missing" ]; then
  echo "Missing system libraries:$missing"
  echo "  Ubuntu/Debian:  sudo apt install libwebkit2gtk-4.1-0 libgtk-3-0"
  echo "  Fedora:         sudo dnf install webkit2gtk4.1 gtk3"
fi
echo "Installed to $BIN/durusql (desktop entry: durusql). Optional for dumps/restores: mariadb-client, postgresql-client."
case ":$PATH:" in *":$BIN:"*) ;; *) echo "Note: add $BIN to your PATH to run 'durusql' from a terminal.";; esac
