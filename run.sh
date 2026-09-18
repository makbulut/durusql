#!/usr/bin/env bash
# One-click launcher for DuruSQL.
#   ./run.sh          build if needed, then start the app
#   ./run.sh dev      hot-reload dev mode (wails dev)
#   ./run.sh build    build only
#   ./run.sh clean    remove build output, deps and generated files
#
# First run installs everything it needs (asks for sudo once for the WebKit dev package),
# and registers a "durusql" entry in the desktop app launcher.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"
export PATH="$HOME/go/bin:/snap/bin:$PATH"

WAILS_VERSION="v2.16.0"
BIN="$ROOT/build/bin/durusql"
LOG="$ROOT/build/run.log"
MODE="${1:-run}"

# ---------------------------------------------------------------- helpers
have_tty() { [ -t 1 ]; }

say() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }

fail() {
    printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2
    if ! have_tty; then
        command -v notify-send >/dev/null && notify-send -u critical "durusql" "$*"
        command -v zenity >/dev/null && zenity --error --width=500 --text="$*" 2>/dev/null || true
    fi
    exit 1
}

# Re-launch ourselves inside a terminal window (used when started from the desktop
# launcher but we need to interact, e.g. for the sudo prompt).
relaunch_in_terminal() {
    for term in gnome-terminal x-terminal-emulator konsole xfce4-terminal xterm; do
        if command -v "$term" >/dev/null; then
            case "$term" in
                gnome-terminal) exec "$term" -- bash -c "'$ROOT/run.sh' '$MODE'; echo; read -rp 'Press Enter to close'" ;;
                *)              exec "$term" -e bash -c "'$ROOT/run.sh' '$MODE'; echo; read -rp 'Press Enter to close'" ;;
            esac
        fi
    done
    fail "Missing system packages and no terminal found. Run ./run.sh from a terminal."
}

# ---------------------------------------------------------------- system deps
WEBKIT_TAG=""
ensure_system_deps() {
    if pkg-config --exists gtk+-3.0 webkit2gtk-4.0 2>/dev/null; then
        return
    fi
    if pkg-config --exists gtk+-3.0 webkit2gtk-4.1 2>/dev/null; then
        WEBKIT_TAG="webkit2_41"
        return
    fi

    have_tty || relaunch_in_terminal

    local pkgs=(build-essential pkg-config libgtk-3-dev)
    if apt-cache show libwebkit2gtk-4.1-dev >/dev/null 2>&1; then
        pkgs+=(libwebkit2gtk-4.1-dev)
    else
        pkgs+=(libwebkit2gtk-4.0-dev)
    fi
    say "Installing system packages (sudo): ${pkgs[*]}"
    sudo apt-get install -y "${pkgs[@]}" || fail "apt-get install failed"

    if pkg-config --exists webkit2gtk-4.1 2>/dev/null && ! pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
        WEBKIT_TAG="webkit2_41"
    fi
}

ensure_toolchain() {
    command -v go   >/dev/null || fail "Go is not installed (sudo apt install golang-go, or sudo snap install go --classic)"
    command -v npm  >/dev/null || fail "npm is not installed (sudo apt install nodejs npm)"
    if ! command -v wails >/dev/null; then
        say "Installing Wails CLI $WAILS_VERSION"
        go install "github.com/wailsapp/wails/v2/cmd/wails@$WAILS_VERSION" || fail "wails install failed"
    fi
}

ensure_frontend_deps() {
    if [ ! -d frontend/node_modules ] || [ frontend/package.json -nt frontend/node_modules/.package-lock.json ]; then
        say "Installing frontend dependencies"
        (cd frontend && npm install --no-audit --no-fund) || fail "npm install failed"
    fi
    # main.go embeds frontend/dist; it has to exist before Go can even parse the package.
    mkdir -p frontend/dist
    [ -n "$(ls -A frontend/dist)" ] || touch frontend/dist/.gitkeep
}

ensure_go_deps() {
    if [ go.mod -nt go.sum ] || [ ! -d "$(go env GOMODCACHE)/github.com/wailsapp" ]; then
        say "Downloading Go modules"
        go mod download || fail "go mod download failed"
    fi
}

# True when the binary is missing or any source file is newer than it.
needs_build() {
    [ -x "$BIN" ] || return 0
    local newer
    newer=$(find . \
        \( -path ./build -o -path ./frontend/node_modules -o -path ./frontend/dist -o -path ./frontend/src/wailsjs \) -prune -o \
        -type f -newer "$BIN" -print -quit)
    [ -n "$newer" ]
}

# Print the underlying Go compile error (wails build hides it unless -v 2, which also dumps the env).
show_compile_error() {
    say "Compile error:"
    go build -tags "desktop,production${WEBKIT_TAG:+,$WEBKIT_TAG}" -o /dev/null . 2>&1 | head -40
}

build() {
    say "Building DuruSQL"
    mkdir -p build
    if have_tty; then
        wails build -ldflags "-X main.version=$(cat "$ROOT/VERSION" 2>/dev/null || echo dev) -X main.updateSource=$(cat "$ROOT/UPDATE_SOURCE" 2>/dev/null || true)" ${WEBKIT_TAG:+-tags $WEBKIT_TAG} || { show_compile_error; fail "build failed"; }
    else
        wails build -ldflags "-X main.version=$(cat "$ROOT/VERSION" 2>/dev/null || echo dev) -X main.updateSource=$(cat "$ROOT/UPDATE_SOURCE" 2>/dev/null || true)" ${WEBKIT_TAG:+-tags $WEBKIT_TAG} >"$LOG" 2>&1 || { show_compile_error >>"$LOG" 2>&1; fail "build failed, see $LOG"; }
    fi
}

install_desktop_entry() {
    local dir="$HOME/.local/share/applications" file
    file="$dir/durusql.desktop"
    [ -f "$file" ] && grep -q "Exec=\"$ROOT/run.sh\"" "$file" && return
    mkdir -p "$dir"
    cat >"$file" <<DESKTOP
[Desktop Entry]
Type=Application
Name=DuruSQL
Comment=Lightweight database client
Exec="$ROOT/run.sh"
Path=$ROOT
Icon=office-database
Terminal=false
Categories=Development;Database;
StartupNotify=true
StartupWMClass=durusql
DESKTOP
    chmod +x "$file"
    command -v update-desktop-database >/dev/null && update-desktop-database "$dir" 2>/dev/null || true
    say "Registered 'DuruSQL' in the app launcher ($file)"
}

# ---------------------------------------------------------------- main
case "$MODE" in
    clean)
        rm -rf build frontend/dist frontend/node_modules frontend/src/wailsjs
        say "Cleaned"
        ;;
    dev)
        ensure_system_deps; ensure_toolchain; ensure_frontend_deps; ensure_go_deps
        exec wails dev ${WEBKIT_TAG:+-tags $WEBKIT_TAG}
        ;;
    build)
        ensure_system_deps; ensure_toolchain; ensure_frontend_deps; ensure_go_deps
        build
        ;;
    run)
        ensure_system_deps; ensure_toolchain; ensure_frontend_deps; ensure_go_deps
        install_desktop_entry
        needs_build && build
        say "Starting DuruSQL"
        exec "$BIN"
        ;;
    *)
        echo "usage: $0 [run|dev|build|clean]" >&2; exit 2 ;;
esac
