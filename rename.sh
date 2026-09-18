#!/usr/bin/env bash
# Renames the product. Usage: ./rename.sh "Display Name" slug   e.g. ./rename.sh "Tablo" tablo
# Touches: Go module + imports, config dir constant (old dir is migrated on first start), window
# title, wails.json, run.sh, package.sh, publish.sh, install script, desktop file, icon, README,
# CHANGELOG, frontend title. Old name is read from wails.json.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
NEW="${1:?display name}"; SLUG="${2:?slug (lowercase, no spaces)}"
OLD_SLUG=$(python3 -c "import json;print(json.load(open('wails.json'))['name'])")
OLD_NAME=$(grep -oP 'AppName = "\K[^"]+' internal/config/config.go)
[ "$SLUG" = "$OLD_SLUG" ] && { echo "already $SLUG"; exit 0; }
echo "renaming $OLD_NAME ($OLD_SLUG) → $NEW ($SLUG)"
# Go
grep -rl --include=*.go "\"$OLD_SLUG/internal/" . | xargs -r sed -i "s#\"$OLD_SLUG/internal/#\"$SLUG/internal/#g"
sed -i "s/^module $OLD_SLUG\$/module $SLUG/" go.mod
sed -i "s/AppName = \"$OLD_NAME\"/AppName = \"$NEW\"/; s/AppDir  = \"$OLD_SLUG\"/AppDir  = \"$SLUG\"/" internal/config/config.go
# keep migrating from every earlier directory name
sed -i "s#if old := filepath.Join(base, \"[a-z]*\"); dirExists(old) {#if old := filepath.Join(base, \"$OLD_SLUG\"); dirExists(old) {#" internal/config/config.go
sed -i "s/Title:\( *\)\"$OLD_NAME\"/Title:\1\"$NEW\"/" main.go
sed -i "s/\"$OLD_SLUG-dumps\"/\"$SLUG-dumps\"/; s/\"$OLD_SLUG-update\"/\"$SLUG-update\"/; s/no $OLD_SLUG binary/no $SLUG binary/; s/== \"$OLD_SLUG\" {/== \"$SLUG\" {/; s/\"$OLD_SLUG.exe\"/\"$SLUG.exe\"/; s/no $OLD_SLUG.exe/no $SLUG.exe/; s/$OLD_SLUG-updater/$SLUG-updater/" app.go internal/update/update.go
# scripts, packaging, docs, frontend
for f in wails.json run.sh package.sh publish.sh packaging/install.sh packaging/*.desktop README.md CHANGELOG.md frontend/index.html frontend/src/App.svelte frontend/src/lib/UpdateDialog.svelte; do
  [ -f "$f" ] || continue
  sed -i "s/\b$OLD_SLUG\b/$SLUG/g; s/${OLD_SLUG}_/${SLUG}_/g; s/\b$OLD_NAME\b/$NEW/g; s/${OLD_SLUG^^}_/${SLUG^^}_/g" "$f"
done
git mv -k packaging/$OLD_SLUG.desktop packaging/$SLUG.desktop 2>/dev/null || mv packaging/$OLD_SLUG.desktop packaging/$SLUG.desktop
git mv -k packaging/$OLD_SLUG.svg packaging/$SLUG.svg 2>/dev/null || mv packaging/$OLD_SLUG.svg packaging/$SLUG.svg
gofmt -l . >/dev/null; go vet ./... && echo "ok — now: ./run.sh build   (the old ~/.config/$OLD_SLUG is migrated to ~/.config/$SLUG on first start)"
