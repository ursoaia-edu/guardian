#!/bin/bash
# Runs once, the first time the postgres container initialises an empty data
# volume (docker-entrypoint-initdb.d convention). It creates guardian_app —
# the DML-only role the running service connects as via DATABASE_URL — with
# the password GUARDIAN_APP_PASSWORD names, which must be set in .env and
# must match the password inside DATABASE_URL. guardian_owner itself is
# created by the postgres image's own POSTGRES_USER/POSTGRES_PASSWORD
# bootstrap; see docker-compose.yml.
set -euo pipefail

: "${GUARDIAN_APP_PASSWORD:?GUARDIAN_APP_PASSWORD must be set in .env}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    CREATE ROLE guardian_app LOGIN PASSWORD '${GUARDIAN_APP_PASSWORD}';
    GRANT CONNECT ON DATABASE ${POSTGRES_DB} TO guardian_app;
    GRANT USAGE ON SCHEMA public TO guardian_app;
EOSQL
