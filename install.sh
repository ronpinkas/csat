#!/bin/sh
# CSAT one-line installer.
#
#   curl -fsSL https://raw.githubusercontent.com/ronpinkas/csat/main/install.sh | sudo sh
#
# Detects your OS/arch, downloads the matching release, verifies its SHA-256,
# and installs it. Options (env vars):
#   CSAT_VERSION=v1.0.0      pin a version (default: latest stable release)
#   CSAT_AUTOUPDATE=1        enable the nightly auto-update timer (off by default)
set -eu

REPO="ronpinkas/csat"
VERSION="${CSAT_VERSION:-latest}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# --- detect platform ---
os=$(uname -s)
arch=$(uname -m)
case "$os" in
  Linux)  goos=linux ;;
  Darwin) goos=darwin ;;
  *) die "unsupported OS '$os'. For Windows, download the zip from https://github.com/$REPO/releases" ;;
esac
case "$arch" in
  x86_64 | amd64)  goarch=amd64 ;;
  aarch64 | arm64) goarch=arm64 ;;
  *) die "unsupported architecture '$arch'" ;;
esac

asset="csat-${goos}-${goarch}.tar.gz"
if [ "$VERSION" = "latest" ]; then
  base="https://github.com/${REPO}/releases/latest/download"
else
  base="https://github.com/${REPO}/releases/download/${VERSION}"
fi

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "Downloading ${asset} (${VERSION}) ..."
curl -fSL "${base}/${asset}" -o "$tmp/$asset" \
  || die "download failed — does the release exist? (for a pre-release set CSAT_VERSION=v1.0.0-rc1)"
curl -fSL "${base}/${asset}.sha256" -o "$tmp/$asset.sha256" || die "checksum download failed"

say "Verifying checksum ..."
expected=$(awk '{print $1}' "$tmp/$asset.sha256")
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
[ "$expected" = "$actual" ] || die "checksum mismatch (expected $expected, got $actual)"

say "Extracting ..."
tar xzf "$tmp/$asset" -C "$tmp"
dir="$tmp/csat-${goos}-${goarch}"

# --- need root for the system install ---
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  command -v sudo >/dev/null 2>&1 || die "please run as root, or install sudo"
  SUDO="sudo"
fi

if [ "$goos" = "darwin" ]; then
  say "macOS detected — installing the binary to /usr/local/bin (no service manager here)."
  $SUDO install -m 0755 "$dir/csat" /usr/local/bin/csat
  say "Done. Run:  csat -config config.toml   (see the bundled README for setup)"
  exit 0
fi

say "Installing ..."
cd "$dir"
$SUDO env CSAT_AUTOUPDATE="${CSAT_AUTOUPDATE:-}" sh ./install.sh
