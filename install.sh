#!/bin/sh
set -eu

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
esac

VERSION="${ASTROTUI_VERSION:-latest}"
PREFIX="${PREFIX:-/usr/local/bin}"
REPO="alphabravo-oss/astronomer-tui"

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/$REPO/releases/latest/download/astrotui_${OS}_${ARCH}.tar.gz"
else
  URL="https://github.com/$REPO/releases/download/$VERSION/astrotui_${OS}_${ARCH}.tar.gz"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$URL" | tar -xz -C "$tmp"
install -m 0755 "$tmp/astrotui" "$PREFIX/astrotui"
echo "installed $PREFIX/astrotui"
