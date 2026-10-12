#!/bin/sh
set -eu

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
esac
case "$OS/$ARCH" in
  linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) ;;
  *) echo "unsupported platform: $OS/$ARCH" >&2; exit 1 ;;
esac

VERSION="${ASTROTUI_VERSION:-latest}"
PREFIX="${PREFIX:-/usr/local/bin}"
REPO="alphabravo-oss/astronomer-tui"
if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/$REPO/releases/latest/download"
else
  URL="https://github.com/$REPO/releases/download/$VERSION"
fi
archive="astrotui_${OS}_${ARCH}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$URL/$archive" -o "$tmp/$archive"
curl -fsSL "$URL/checksums.txt" -o "$tmp/checksums.txt"
command -v cosign >/dev/null 2>&1 || { echo "cosign is required to verify the release signature" >&2; exit 1; }
curl -fsSL "$URL/checksums.txt.sig" -o "$tmp/checksums.txt.sig"
curl -fsSL "$URL/checksums.txt.pem" -o "$tmp/checksums.txt.pem"
identity='^https://github\.com/alphabravo-oss/astronomer-tui/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$'
identity_flag="--certificate-identity-regexp"
if [ "$VERSION" != "latest" ]; then
  identity_flag="--certificate-identity"
  identity="https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$VERSION"
fi
cosign verify-blob --signature "$tmp/checksums.txt.sig" --certificate "$tmp/checksums.txt.pem" \
  "$identity_flag" "$identity" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" "$tmp/checksums.txt"
expected="$(awk -v name="$archive" '$2 == name {print $1}' "$tmp/checksums.txt")"
if [ "${#expected}" -ne 64 ]; then
  echo "missing or invalid checksum for $archive" >&2; exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
else
  echo "sha256sum or shasum is required" >&2; exit 1
fi
if [ "$expected" != "$actual" ]; then
  echo "checksum verification failed for $archive" >&2; exit 1
fi
tar -xzf "$tmp/$archive" -C "$tmp" astrotui
install -m 0755 "$tmp/astrotui" "$PREFIX/astrotui"
echo "installed $PREFIX/astrotui"
