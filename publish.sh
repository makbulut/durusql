#!/usr/bin/env bash
# Publishes dist/ as a GitHub release (needs the gh CLI, logged in) or copies it to a web server.
#   ./publish.sh 0.6.0-beta.1          → gh release create v0.6.0-beta.1 --prerelease dist/*
#   DURUSQL_PUBLISH=user@host:/var/www/durusql ./publish.sh 0.6.0   → rsync dist/ there (self-hosted JSON channel)
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
VERSION="${1:-$(cat VERSION)}"
[ -d dist ] || { echo "run ./package.sh $VERSION first"; exit 1; }
if [ -n "${DURUSQL_PUBLISH:-}" ]; then
  rsync -av --delete dist/ "$DURUSQL_PUBLISH/"
  echo "published to $DURUSQL_PUBLISH (channel files: stable.json / beta.json)"
  exit 0
fi
command -v gh >/dev/null || { echo "install the GitHub CLI (gh) and run: gh auth login"; exit 1; }
PRE=""; [[ "$VERSION" == *-* ]] && PRE="--prerelease"
NOTES=$(awk -v v="$VERSION" '/^## /{ if (found) exit; if (index($0, v)) { found=1; next } } found' CHANGELOG.md 2>/dev/null || true)
[ -n "$NOTES" ] || NOTES="DuruSQL $VERSION"
git tag -f "v$VERSION" >/dev/null 2>&1 || true
gh release create "v$VERSION" $PRE --title "DuruSQL $VERSION" --notes "$NOTES" dist/*.deb dist/*.tar.gz dist/*.zip dist/SHA256SUMS $(ls dist/*.exe dist/*.dmg 2>/dev/null || true)
echo "released v$VERSION ($( [ -n "$PRE" ] && echo beta || echo stable ) channel)"
