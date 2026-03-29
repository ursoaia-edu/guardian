#!/bin/bash
# Requires superuser privileges

if [ "$EUID" -ne 0 ]; then
  echo "Switching to root..."
  exec sudo -i bash "$0" "$@"
fi

mkdir -p /usr/local/bin/guardian
tar -xzvf procsentinel-server.tar.gz -C /usr/local/bin/guardian
cp /usr/local/bin/guardian/guardian.service /etc/systemd/system/guardian.service
systemctl daemon-reload
systemctl enable guardian
systemctl start guardian
systemctl restart guardian
systemctl status guardian
