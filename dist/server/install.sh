#!/bin/bash
# Requires superuser privileges
#
# Prerequisite: guardian_owner and guardian_app must already exist in the
# target Postgres database, and .env (copied from server.env below) must
# already carry real DATABASE_URL / MIGRATE_DATABASE_URL values. See
# specs/server.md's "Production role prerequisites" for the exact CREATE
# ROLE statements — this script does not create them, because their
# passwords must not live in the repository.

if [ "$EUID" -ne 0 ]; then
  echo "Switching to root..."
  exec sudo -i bash "$0" "$@"
fi

mkdir -p /usr/local/bin/guardian
cp server.env /usr/local/bin/guardian/.env
cp guardian-server /usr/local/bin/guardian/guardian-server
cp guardian-server.service /etc/systemd/system/guardian-server.service

# guardian-server migrate reads MIGRATE_DATABASE_URL from the process
# environment directly, not from .env — only the normal (non-migrate)
# startup path loads .env (see server/main.go) — so it is exported here
# before the subcommand runs. Run once per deploy, before the service
# using DATABASE_URL is (re)started: the service's own role cannot alter
# the schema at all (see specs/server.md's Row-Level Security section).
set -a
source /usr/local/bin/guardian/.env
set +a
if ! /usr/local/bin/guardian/guardian-server migrate; then
  echo "guardian-server migrate failed; not starting the service." >&2
  exit 1
fi

systemctl daemon-reload
systemctl enable guardian-server
systemctl start guardian-server
systemctl restart guardian-server
systemctl status guardian-server
