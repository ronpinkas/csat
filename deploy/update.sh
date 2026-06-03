#!/bin/sh
# CSAT auto-updater (installed as /usr/local/bin/csat-update, run by the
# csat-update.timer). Updates the binary to the latest release within the
# tracked major version, backing up the database first.
#
#   CSAT_TRACK=v1   only update within this major line (default v1)
set -eu

REPO="ronpinkas/csat"
TRACK="${CSAT_TRACK:-v1}"
BIN="/usr/local/bin/csat"
DATA="/var/lib/csat"

log() { printf '[csat-update] %s\n' "$*"; }
die() { printf '[csat-update] error: %s\n' "$*" >&2; exit 1; }

case "$(uname -m)" in
  x86_64 | amd64)  goarch=amd64 ;;
  aarch64 | arm64) goarch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
asset="csat-linux-${goarch}.tar.gz"

current=$("$BIN" -version 2>/dev/null || echo none)

# latest stable tag (GitHub excludes pre-releases from releases/latest)
latest=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
[ -n "$latest" ] || die "could not determine latest release"

# only auto-update within the tracked major (e.g. v1.*)
case "$latest" in
  "${TRACK}".*) ;;
  *) log "latest $latest is outside track $TRACK — skipping (upgrade manually)"; exit 0 ;;
esac

if [ "$latest" = "$current" ]; then
  log "already up to date ($current)"
  exit 0
fi
log "updating $current -> $latest"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
base="https://github.com/${REPO}/releases/download/${latest}"
curl -fsSL "${base}/${asset}" -o "$tmp/$asset" || die "download failed"
curl -fsSL "${base}/${asset}.sha256" -o "$tmp/$asset.sha256" || die "checksum download failed"
expected=$(awk '{print $1}' "$tmp/$asset.sha256")
actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
[ "$expected" = "$actual" ] || die "checksum mismatch"
tar xzf "$tmp/$asset" -C "$tmp"
newbin="$tmp/csat-linux-${goarch}/csat"
[ -x "$newbin" ] || die "binary missing in archive"

# back up the database before swapping
if [ -f "$DATA/csat.db" ]; then
  mkdir -p "$DATA/backups"
  stamp=$(date +%Y%m%d-%H%M%S)
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "$DATA/csat.db" ".backup '$DATA/backups/csat-$stamp.db'" || cp "$DATA/csat.db" "$DATA/backups/csat-$stamp.db"
  else
    cp "$DATA/csat.db" "$DATA/backups/csat-$stamp.db"
  fi
  log "backed up DB to $DATA/backups/csat-$stamp.db"
fi

# atomic swap + restart (migrations apply on boot)
install -m 0755 "$newbin" "${BIN}.new"
mv "${BIN}.new" "$BIN"
systemctl restart csat
log "updated to $latest and restarted"
