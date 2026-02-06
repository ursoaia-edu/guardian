#!/bin/bash
# Requires superuser privileges

if [ "$EUID" -ne 0 ]; then
  echo "Switching to root..."
  exec sudo -i bash "$0" "$@"
fi

mkdir -p /usr/local/bin/guardian
cp server.env -C /usr/local/bin/guardian/.env
cp /usr/local/bin/guardian/guardian-server.service /etc/systemd/system/guardian-server.service
systemctl daemon-reload
systemctl enable guardian-server
systemctl start guardian-server
systemctl restart guardian-server
systemctl status guardian-server
