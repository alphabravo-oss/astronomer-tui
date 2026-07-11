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
	BASE_URL="https://github.com/$REPO/releases/latest/download"
else
	BASE_URL="https://github.com/$REPO/releases/download/$VERSION"
fi

ARCHIVE="astrotui_${OS}_${ARCH}.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! command -v cosign >/dev/null 2>&1; then
	echo "cosign is required to verify astrotui releases: https://docs.sigstore.dev/cosign/system_config/installation/" >&2
	exit 1
fi

curl -fsSL "$BASE_URL/$ARCHIVE" -o "$tmp/$ARCHIVE"
curl -fsSL "$BASE_URL/checksums.txt" -o "$tmp/checksums.txt"
curl -fsSL "$BASE_URL/checksums.txt.sig" -o "$tmp/checksums.txt.sig"
curl -fsSL "$BASE_URL/checksums.txt.pem" -o "$tmp/checksums.txt.pem"

cosign verify-blob \
	--certificate "$tmp/checksums.txt.pem" \
	--signature "$tmp/checksums.txt.sig" \
	--certificate-identity-regexp '^https://github.com/alphabravo-oss/astronomer-tui/.github/workflows/release.yml@refs/tags/v' \
	--certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
	"$tmp/checksums.txt" >/dev/null

expected="$(awk -v name="$ARCHIVE" '$2 == name { print $1 }' "$tmp/checksums.txt")"
if [ -z "$expected" ]; then
	echo "release checksum does not include $ARCHIVE" >&2
	exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$tmp/$ARCHIVE" | awk '{ print $1 }')"
else
	actual="$(shasum -a 256 "$tmp/$ARCHIVE" | awk '{ print $1 }')"
fi
if [ "$actual" != "$expected" ]; then
	echo "checksum verification failed for $ARCHIVE" >&2
	exit 1
fi

tar -xzf "$tmp/$ARCHIVE" -C "$tmp"
install -m 0755 "$tmp/astrotui" "$PREFIX/astrotui"
echo "installed $PREFIX/astrotui"
