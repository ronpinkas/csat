#!/bin/sh
# Turnkey installer for the CSAT single-binary deployment.
# Run as root from inside the unpacked release directory:  sudo ./install.sh
#
# Uses bundled config.toml / csat.env / logo.* if present (a customer bundle),
# otherwise falls back to the *.example templates (the generic package).
set -eu

BIN=/usr/local/bin/csat
CONF=/etc/csat
DATA=/var/lib/csat
UNIT=/etc/systemd/system/csat.service

if [ "$(id -u)" -ne 0 ]; then
  echo "Please run as root (sudo ./install.sh)" >&2
  exit 1
fi

# service user/group
NOLOGIN=$(command -v nologin || echo /sbin/nologin)
getent group csat >/dev/null 2>&1 || groupadd --system csat
id csat >/dev/null 2>&1 || useradd --system -g csat --no-create-home --shell "$NOLOGIN" csat

# binary
install -m 0755 ./csat "$BIN"

# directories
mkdir -p "$CONF" "$DATA"
chmod 0755 "$CONF"
chown csat:csat "$DATA"
chmod 0750 "$DATA"

# config.toml — read by the csat process, so it must be csat-readable
if [ ! -f "$CONF/config.toml" ]; then
  [ -f ./config.toml ] && SRC=./config.toml || SRC=./config.example.toml
  install -m 0640 -o root -g csat "$SRC" "$CONF/config.toml"
  echo "wrote $CONF/config.toml  ($SRC)"
fi

# csat.env — read by systemd (root) only, keep it root-private
if [ ! -f "$CONF/csat.env" ]; then
  [ -f ./csat.env ] && SRC=./csat.env || SRC=./.env.example
  install -m 0600 -o root -g root "$SRC" "$CONF/csat.env"
  echo "wrote $CONF/csat.env  ($SRC)"
fi

# logo (optional) — served by the csat process, so world-readable is fine
for L in logo.svg logo.png logo.webp logo.jpg logo.jpeg logo.gif logo.bmp; do
  if [ -f "./$L" ]; then
    install -m 0644 -o root -g csat "./$L" "$CONF/$L"
    echo "installed logo $CONF/$L"
  fi
done

# systemd unit
install -m 0644 ./csat.service "$UNIT"
systemctl daemon-reload

# auto-updater (when bundled): installs the updater + nightly timer
AUTOUPDATE_NOTE=""
if [ -f ./update.sh ] && [ -f ./csat-update.service ] && [ -f ./csat-update.timer ]; then
  install -m 0755 ./update.sh /usr/local/bin/csat-update
  install -m 0644 ./csat-update.service /etc/systemd/system/csat-update.service
  install -m 0644 ./csat-update.timer /etc/systemd/system/csat-update.timer
  systemctl daemon-reload
  if [ "${CSAT_NO_AUTOUPDATE:-}" = "1" ]; then
    AUTOUPDATE_NOTE="auto-update installed but disabled — enable with: systemctl enable --now csat-update.timer"
  else
    systemctl enable --now csat-update.timer >/dev/null 2>&1 || true
    AUTOUPDATE_NOTE="auto-update enabled (nightly) — disable with: systemctl disable --now csat-update.timer"
  fi
fi

echo
echo "Installed. Next:"
echo "  1. review $CONF/config.toml and $CONF/csat.env"
echo "  2. systemctl enable --now csat"
echo "  3. curl -fsS http://127.0.0.1:8080/healthz   # -> ok"
echo "  4. put this host behind your reverse proxy (see nginx-csat.conf.example / apache-csat.conf.example)"
[ -n "$AUTOUPDATE_NOTE" ] && echo "  * $AUTOUPDATE_NOTE"
