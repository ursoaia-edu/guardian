#!/bin/sh
# Deploy order: run "./guardian-server migrate" once with MIGRATE_DATABASE_URL
# set (guardian-server reads .env from its working directory, so an .env
# there is enough), then start the service. The service itself cannot alter
# the schema.
#
# Built static (CGO_ENABLED=0) on purpose: dist/server/Dockerfile runs this
# binary on Alpine, whose musl libc cannot load a glibc-linked executable —
# the failure there is a bare "not found" that points nowhere. GOARCH
# defaults to the machine running this script; building on an arm64 laptop
# for an x86-64 server is "GOARCH=amd64 ./build.sh".
set -e
cd "$(dirname "$0")"

CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH:-$(go env GOARCH)}" \
  go build -trimpath -ldflags="-s -w" -o guardian-server

cp guardian-server ../dist/server/
rm guardian-server
