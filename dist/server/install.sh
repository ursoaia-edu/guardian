#!/bin/bash
# Requires superuser privileges
#
# Prerequisite: guardian_owner and guardian_app must already exist in the
# target Postgres database, and server.env in this directory must already
# carry real DATABASE_URL / MIGRATE_DATABASE_URL values (it becomes .env on
# the first install; see below). See specs/server.md's "Production role
# prerequisites" for the exact CREATE ROLE statements — this script does not
# create them, because their passwords must not live in the repository.
#
# Bare-metal installs have nothing in front of the server, so TRUSTED_PROXIES
# stays 0 unless you put a reverse proxy (Caddy, nginx) in front yourself —
# in which case set it to 1 in .env, or the rate limiter keys every client
# on the proxy's address.
set -euo pipefail

if [ "$EUID" -ne 0 ]; then
  echo "Switching to root..."
  exec sudo -i bash "$0" "$@"
fi

cd "$(dirname "$0")"
INSTALL_DIR=/usr/local/bin/guardian

# The service account guardian-server.service runs as. No shell, no home:
# it exists only so the process is not root.
if ! id -u guardian >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin guardian
fi

mkdir -p "$INSTALL_DIR"

# .env is written once. A re-run for an upgrade must not overwrite the
# passwords an operator edited in place with the CHANGE_ME template.
if [ ! -f "$INSTALL_DIR/.env" ]; then
  cp server.env "$INSTALL_DIR/.env"
else
  echo "Keeping the existing $INSTALL_DIR/.env (server.env was not copied over it)."
fi
# The database passwords live in this file; only the service account and
# root may read it.
chown root:guardian "$INSTALL_DIR/.env"
chmod 640 "$INSTALL_DIR/.env"

install -o root -g root -m 0755 guardian-server "$INSTALL_DIR/guardian-server"
install -o root -g root -m 0644 guardian-server.service /etc/systemd/system/guardian-server.service

# Run once per deploy, before the service using DATABASE_URL is (re)started:
# the service's own role cannot alter the schema at all (see specs/server.md's
# Row-Level Security section). guardian-server reads .env from its working
# directory, so the subcommand runs from the install directory.
if ! (cd "$INSTALL_DIR" && ./guardian-server migrate); then
  echo "guardian-server migrate failed; not starting the service." >&2
  exit 1
fi

systemctl daemon-reload
systemctl enable guardian-server
systemctl restart guardian-server
systemctl status guardian-server --no-pager
